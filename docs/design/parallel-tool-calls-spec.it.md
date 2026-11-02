# Parallel Tool Calling — Specifica tecnica

Stato: implementata (fasi 1–4 del §6). Il comportamento che è stato rilasciato è descritto in [`../architecture/parallel-tool-calls.md`](../architecture/parallel-tool-calls.md); `max_parallel_tool_calls` ha default `4`. Ispirata a Hermes Agent (`agent/tool_dispatch_helpers.py`: `plan_tool_batch_segments`, `execute_tool_calls_concurrent`, `execute_tool_calls_segmented`), adattata all'architettura di PicoClaw. Non è un porting: dove PicoClaw ha già un meccanismo (hook, steering, loop detection) la specifica lo preserva.

## 1. Problema

Quando il modello emette più tool call nella stessa risposta, oggi PicoClaw le esegue così:

| Percorso | Dove | Comportamento attuale |
| --- | --- | --- |
| Loop principale | `Pipeline.ExecuteTools`, `pkg/agent/pipeline_execute.go:110` | Strettamente sequenziale. Dopo ogni tool controlla steering e graceful interrupt; le chiamate rimanenti ricevono `Skipped due to queued user message.` |
| Subagent (fallback legacy) | `RunToolLoop`, `pkg/tools/toolloop.go:161` | Tutte le chiamate in parallelo (max 8, `maxParallelToolCalls`), **senza alcuna classificazione**. `write_file` + `read_file` sullo stesso path o due `exec` corrono in race. |

Due problemi distinti:

1. Il loop principale spreca latenza: `web_search` ×3 o `read_file` ×5 vengono serializzati senza motivo.
2. Il percorso subagent è parallelo ma non sicuro.

La soluzione è un unico scheduler statico condiviso dai due percorsi.

## 2. Obiettivi e non-obiettivi

Obiettivi:

- Eseguire in parallelo solo chiamate **consecutive e dichiaratamente compatibili** nello stesso batch.
- Fail-closed: un tool che non dichiara nulla è una barriera sequenziale.
- Ordine dei messaggi `tool` nella conversazione e nella sessione identico all'ordine delle chiamate, indipendentemente da quale finisca prima.
- Nessuna regressione su hook, steering, loop detection, hard abort, eventi runtime.
- Kill switch: con parallelismo disabilitato il comportamento è identico a oggi.

Non-obiettivi:

- Riordinare il batch o dedurre dipendenze semantiche tra risultati (come Hermes: se `web_fetch` dipende da `web_search`, il modello deve emetterli in turni diversi).
- Parallelismo tra iterazioni LLM diverse.
- Annullare tool già avviati quando arriva uno steering (vedi §4.6).
- Parallelismo per tool MCP in v1 (vedi §4.1).

## 3. Cosa prendiamo da Hermes e cosa cambia

| Hermes | PicoClaw |
| --- | --- |
| Whitelist centrale di nomi (`web_search`, `read_file`, …) e lista `NEVER_PARALLEL_TOOLS` | Interfaccia opzionale implementata dal tool (§4.1). Funziona per tool plugin/nascosti/rinominati; default = sequenziale. |
| `execution_cwd` per risolvere i path relativi | Il tool risolve i path contro il proprio workspace (stessa logica di `fs`, symlink inclusi). |
| `batch_admission` controlla JSON valido e timeout estesi | Gli argomenti arrivano già parsati (`tc.Arguments`); argomenti non interpretabili ⇒ il tool risponde `Safe: false`. |
| Nessun hook | `BeforeTool`/`ApproveTool`/`AfterTool` possono riscrivere, negare, rispondere al posto del tool. La classificazione deve sopravvivere a questo (§4.4). |
| Nessuno steering | Il polling avviene ai confini di segmento (§4.6). |
| Nomi tool Hermes (`web_extract`, `vision_analyze`, `clarify`) | Mapping ai tool reali nella tabella §4.1. `clarify` non esiste in PicoClaw. |

## 4. Design

### 4.1 Classificazione dei tool

Nuova interfaccia opzionale in `pkg/tools/shared/parallel.go` (con alias in `shared_facade.go`):

```go
// ParallelSafety describes whether one specific call can run concurrently
// with its neighbours in the same batch.
type ParallelSafety struct {
	Safe  bool         // false => sequential barrier
	Paths []PathAccess // filesystem footprint; empty = none
}

type PathAccess struct {
	Path  string // absolute, symlink-resolved
	Write bool
}

// ParallelAware is implemented by tools that can declare parallel safety.
// Tools that do not implement it are always sequential.
type ParallelAware interface {
	Tool
	ParallelSafety(args map[string]any) ParallelSafety
}
```

La valutazione è **per chiamata** (riceve gli argomenti) perché il footprint dipende dal path.

Assegnazione iniziale:

| Tool | Classe | Footprint |
| --- | --- | --- |
| `web_search`, `web_fetch`, `find_skills` | parallel-safe | nessuno |
| `image_generate` | parallel-safe | nessuno |
| `read_file`, `load_image` | path-scoped | read `path` |
| `list_dir` | path-scoped | read `path` (directory: copre tutto il sottoalbero) |
| `search_files` | path-scoped | read `path` (default workspace root, sottoalbero) |
| `write_file`, `edit_file`, `append_file` | path-scoped | write `path` |
| `exec`, sessioni processo, `cron`, `todo`, `spawn`, `spawn_status`, `subagent`, `delegate`, `install_skill`, `clawhub`, hardware (`i2c`, `spi`, `serial`) | sequenziale | — (side effect, stato condiviso o hardware) |
| `message`, `reaction`, `send_file`, `send_tts` | sequenziale | — (l'ordine di consegna all'utente è osservabile) |
| tool MCP (`mcp_*`) | sequenziale | — |
| tool sconosciuto / non registrato | sequenziale | — (l'errore compare in ordine) |

MCP: la bozza originale era incoerente (sezione 1 "solo sequenziale", planner "se dichiara supporto"). Decisione: **v1 sempre sequenziale**. Un eventuale opt-in futuro sarà per-server in config (`parallel_safe: true`), non basato su `ReadOnlyHint`, che per specifica MCP è un'indicazione non fidata.

Ogni tool marcato safe deve essere stato verificato come goroutine-safe (nessuno stato mutabile condiviso non protetto) e passare `go test -race` con due esecuzioni concorrenti.

### 4.2 Regole di collisione sui path

Per ogni chiamata il tool restituisce path assoluti. Risoluzione: join con il workspace se relativo, `filepath.Clean`, poi risoluzione dei symlink sull'antenato esistente più vicino (stesso meccanismo di `resolvePathAgainstExistingAncestor`, `pkg/tools/fs/filesystem.go:279`, così funziona anche per file non ancora esistenti).

Due path si **sovrappongono** se uguali o se uno è antenato dell'altro (directory vs file contenuto). Il confronto è case-insensitive su tutte le piattaforme: un falso conflitto costa solo parallelismo, un conflitto mancato su un filesystem case-insensitive corrompe i dati.

Due accessi **confliggono** se si sovrappongono e almeno uno è write:

| Caso | Esito |
| --- | --- |
| read A, read A | parallelo |
| read A, read B | parallelo |
| read A, write A | sequenziale |
| write A, write A | sequenziale |
| write A, write B | parallelo |
| `search_files` su `.` , write su qualunque file del workspace | sequenziale (antenato) |
| path non risolvibile, non stringa, errore di symlink | `Safe: false` ⇒ barriera |

### 4.3 Planner

File nuovo `pkg/tools/schedule.go` (package `tools`, così lo usano sia `agent` sia `RunToolLoop`). Funzione pura, nessuna esecuzione:

```go
// Segment is a run of calls executed together (Parallel) or one by one.
// Calls holds indexes into the original batch, in original order.
type Segment struct {
	Parallel bool
	Calls    []int
}

func PlanBatch(reg *ToolRegistry, calls []providers.ToolCall) []Segment
```

Algoritmo:

```
segments, group, reserved := [], [], []
flush():
    len(group) >= 2  -> segments += Segment{Parallel: true, group}
    len(group) == 1  -> appendSequential(group[0])      // un parallelo da 1 non serve
    group, reserved = [], []
for i, call in calls:
    fp := classify(reg, call)                // ParallelAware o {Safe:false}
    if !fp.Safe:                             // barriera
        flush(); appendSequential(i); continue
    if conflicts(reserved, fp.Paths):        // collisione: chiude il gruppo,
        flush()                              // la chiamata ne apre uno nuovo
    group += i; reserved += fp.Paths
flush()
```

`appendSequential` fonde segmenti sequenziali adiacenti. Invarianti: l'ordine degli indici concatenati dei segmenti è sempre `0..n-1`; nessuna chiamata supera una barriera precedente; un segmento parallelo ha ≥ 2 chiamate e nessuna coppia in conflitto.

Esempi (i nomi sono i tool PicoClaw):

```
1 web_search  2 read_file(a)  3 exec  4 web_fetch  5 load_image(b)  6 write_file(a)
→ P[1,2]  S[3]  P[4,5,6]            // 6 non confligge con 5 (b ≠ a)

1 read_file(a)  2 read_file(b)  3 write_file(a)  4 read_file(c)
→ P[1,2]  P[3,4]                    // 3 chiude il gruppo, apre il successivo

1 read_file(a)  2 write_file(a)
→ S[1,2]                            // gruppo da 1 + gruppo da 1 → fusi in sequenziale
```

Nota rispetto alla bozza originale: in Hermes `write_file` chiudeva sempre il segmento parallelo. Qui una scrittura è ammessa in parallelo con chiamate non conflittuali (scaffolding di più file in un colpo solo è un pattern comune dei modelli). Se si preferisce la versione prudente basta far restituire `Safe: false` a `write_file`/`edit_file`/`append_file`: nessuna modifica al planner.

Il planner non conosce hook né stato di runtime: lavora sulle chiamate grezze del modello. La rivalidazione dopo gli hook è descritta in §4.4.

### 4.4 Esecuzione: prepare / run / finish

`ExecuteTools` oggi fa tutto in un corpo da ~800 righe per ogni chiamata. Viene spezzato in tre fasi; **solo `run` gira in goroutine**.

| Fase | Contenuto (da `pipeline_execute.go`) | Thread |
| --- | --- | --- |
| `prepare` | deny da turn profile, `BeforeTool` (continue/modify/respond/deny/abort/hard_abort), `ApproveTool`, `feedback.publish`, evento `ToolExecStart`, costruzione `execCtx` | coordinatore, in ordine |
| `run` | solo `Tools.ExecuteWithContext(...)` + misura durata | goroutine (semaforo `max_parallel_tool_calls`) |
| `finish` | check hard abort, `AfterTool`, spill risultato, consegna media/`ForUser`, `ToolExecEnd`, append messaggio + persistenza sessione, `recordLoopCall`, poll steering/graceful, `pendingResults` | coordinatore, **in ordine originale** |

`prepare` produce un `preparedCall` con esito `run`, `respond` (risultato sintetizzato da hook) o `denied` (messaggio di diniego). Gli ultimi due non passano da `run` e sono completati da `finish` con i percorsi di codice esistenti.

Per ogni segmento del piano:

- **Sequenziale**: per ogni chiamata `prepare → run → finish`, come oggi, incluso il poll di steering dopo ogni tool. Con parallelismo disabilitato tutto il batch è un unico segmento sequenziale: comportamento identico all'attuale.
- **Parallelo**: `prepare` di tutti i membri in ordine; **rivalidazione** (sotto); `run` concorrente con semaforo; `wg.Wait()`; `finish` di tutti i membri in ordine.

Rivalidazione: gli hook possono cambiare nome e argomenti (`modify`) o sostituire il risultato. Dopo `prepare` si riesegue `PlanBatch` sulle chiamate post-hook del segmento. Se il risultato non è esattamente un singolo segmento parallelo con tutti i membri `run`, il segmento degrada a sequenziale (`run → finish` uno alla volta, senza rieseguire gli hook). Gli hook possono quindi solo degradare, mai promuovere. Caso raro; l'effetto collaterale accettato è che un eventuale skip da steering mostri `ToolExecStart` seguito da `ToolExecSkipped`.

Concorrenza: `run` ripristina il recover da panic (il registry già lo fa; la goroutine ha un secondo `recover` difensivo come in `RunToolLoop`). Tutte le goroutine ricevono `turnCtx`; l'hard abort lo cancella (`requestHardAbort`) e `finish` lo controlla dopo `wg.Wait()` come oggi dopo ogni tool.

Perché questa forma: `messages`, sessione, `loopDetectionHistory` (non protetto da lock), `exec.allResponsesHandled`, `handledAttachments` e i flag di `turnExecution` restano toccati da un solo goroutine. Nessun lock nuovo.

### 4.5 Ordine di risultati ed eventi

- I messaggi `tool` sono appesi e persistiti nell'ordine originale delle chiamate. La cronologia è deterministica a prescindere dai tempi di completamento.
- `ToolExecStart` è emesso in `prepare` (ordine originale, prima dell'esecuzione del segmento); `ToolExecEnd` in `finish` (ordine originale, dopo il completamento dell'intero segmento). `Duration` è la durata reale del singolo tool.
- I `ForUser` dei tool paralleli sono pubblicati in `finish`, quindi nell'ordine originale.
- `AfterTool` per un membro di un segmento parallelo scatta quando l'intero segmento è terminato, non subito dopo il suo tool. Va documentato nella guida hook.

### 4.6 Interazioni

**Steering e graceful interrupt.** Il poll avviene dopo ogni chiamata in un segmento sequenziale (invariato) e **una volta a fine segmento parallelo** (dopo `finish` di tutti). Un segmento parallelo è atomico: i suoi risultati sono già calcolati e servono per l'accoppiamento `tool_call_id` ↔ messaggio `tool`, quindi non si scartano. Se c'è steering pendente, i segmenti successivi ricevono `Skipped due to queued user message.`. Conseguenza: la latenza dello steering può arrivare fino al tool più lento del segmento. Accettato in v1; la mitigazione futura (non avviare i membri in coda al semaforo quando arriva uno steering) è fuori scope.

**Loop detection.** `recordLoopCall` gira in `finish` in ordine originale: stessa semantica per i contatori. Se un membro porta a `loopStatusCritical`, i membri successivi dello stesso segmento sono già stati eseguiti: i loro risultati reali vengono comunque appesi, poi `skipRemainingForLoop` si applica ai segmenti successivi.

**Hard abort.** Controllato dopo `wg.Wait()`; se attivo, `ToolControlBreak` come oggi (risultati scartati).

**Tool asincroni** (`AsyncExecutor`: `spawn`, `subagent`, …) sono classificati sequenziali: tornano subito con `AsyncResult`, quindi non c'è guadagno e le callback restano per-chiamata come ora.

**Tool feedback.** Nessuna modifica a `toolFeedbackBatch`: viene invocato in `prepare`, in ordine; la modalità compatta (un messaggio per risposta) continua a funzionare.

### 4.7 `RunToolLoop` (subagent)

Il fan-out incondizionato (`toolloop.go:161-212`) viene sostituito da `PlanBatch` + lo stesso esecutore di segmenti (`runSegment`, funzione condivisa in `pkg/tools` che contiene semaforo, recover e raccolta risultati per indice). `ToolLoopConfig` guadagna `MaxParallel int` (`0` = cap di default 8, `1` = sequenziale); il percorso legacy non legge `max_parallel_tool_calls`. Nessun hook/steering in questo percorso, quindi niente `prepare`/`finish`.

È un cambio di comportamento voluto: i tool che non implementano `ParallelAware` ora sono sequenziali anche per i subagent. `TestRunToolLoop_BoundsConcurrentToolExecution` (`toolloop_concurrency_test.go`) va aggiornato: il tool `slow` di test implementa `ParallelAware` con `Safe: true`.

### 4.8 Configurazione

In `AgentDefaults` (`pkg/config/config.go`), accanto a `MaxParallelTurns`, stessa convenzione:

```go
MaxParallelToolCalls int `json:"max_parallel_tool_calls,omitempty" env:"PICOCLAW_AGENTS_DEFAULTS_MAX_PARALLEL_TOOL_CALLS"` // Max concurrent tool calls per batch (0 or 1 = sequential)
```

Default `4` (`pkg/config/defaults.go`); `0` o `1` lo disabilitano e ripristinano il comportamento sequenziale. Un valore > 1 è anche il limite del semaforo (le chiamate in eccesso attendono, non vengono scartate). Valore massimo consigliato: 8 (il valore attuale di `maxParallelToolCalls`).

### 4.9 Osservabilità

Un solo log `Info` per batch dopo `PlanBatch`, solo se il batch ha ≥ 2 chiamate: numero di chiamate, segmenti (`P[web_search,read_file] S[exec] …`), cap. Nessun nuovo evento runtime in v1; se serve distinguere in UI le esecuzioni parallele, aggiungere `Parallel bool` a `ToolExecEndPayload`.

## 5. Piano di test

Planner (`pkg/tools/schedule_test.go`, table-driven, senza I/O):

- Gli esempi di §4.3, più: batch vuoto, singola chiamata, tool sconosciuto, tool che non implementa `ParallelAware`, argomenti malformati, path non risolvibile, path relativo vs assoluto equivalenti, symlink verso lo stesso file, antenato/discendente, case-insensitive su darwin/windows.
- Proprietà: concatenando gli indici dei segmenti si ottiene `0..n-1`; nessun segmento parallelo con < 2 membri; nessuna coppia in conflitto dentro un segmento parallelo.

Esecuzione (`pkg/agent`, con `-race`):

- Primo tool lento e secondo veloce: messaggi `tool` in ordine originale, sia in `messages` sia nello store di sessione.
- Concorrenza massima osservata ≤ `max_parallel_tool_calls`.
- Kill switch (`0`/`1`): sequenza di eventi e messaggi identica al golden dell'esecuzione sequenziale attuale.
- `BeforeTool` con `modify` che trasforma un membro in tool sequenziale ⇒ segmento degradato.
- `BeforeTool` con `respond` e `deny` dentro un segmento parallelo.
- Steering arrivato durante un segmento parallelo ⇒ segmenti successivi skippati, risultati del segmento presenti.
- Hard abort durante un segmento parallelo.
- Loop critico a metà segmento.
- Panic in un tool del segmento non blocca gli altri.

Subagent: `write_file(a)` + `read_file(a)` mai concorrenti; il test di bound sul semaforo aggiornato come in §4.7.

## 6. Rollout

1. **Planner + interfaccia**, implementata dai tool della tabella §4.1, con test. Nessun cambiamento di comportamento.
2. **`RunToolLoop`** passa a `PlanBatch`/`runSegment`. Chiude il problema di sicurezza esistente.
3. **Refactor `ExecuteTools`** in `prepare`/`run`/`finish` con tutto sequenziale (config `0`). Gate: i test esistenti (`pipeline_execute_test.go`, `steering_test.go`, `hooks_test.go`, `loop_detection_test.go`) passano senza modifiche.
4. **Abilitazione parallela** nel loop principale dietro `max_parallel_tool_calls`, con default `4`.
5. (Opzionale) opt-in MCP per-server.

Documentazione da aggiornare: `docs/architecture/steering.md` (poll ai confini di segmento), `docs/architecture/hooks/` (timing di `AfterTool`, ordine hook), `docs/architecture/runtime-events.md` (ordine di Start/End), guida config.

## 7. Rischi

- **Rate limit dei provider di ricerca**: più `web_search` in parallelo possono dare 429 (es. piani gratuiti con 1 req/s). Mitigazioni: cap di default 4 e rotazione chiavi già presente. Se emerge, aggiungere un limite per-tool a `ParallelSafety`.
- **Dispositivi piccoli**: `search_files` e `read_file` paralleli aumentano CPU/RAM di picco su SBC. Il cap è configurabile (`0` o `1` disabilita).
- **Cambi di timing per hook esterni**: gli hook di processo vedono tutti i `BeforeTool` del segmento prima di qualunque esecuzione, e `AfterTool` solo a fine segmento. Va comunicato; chi dipende da lettura-dopo-scrittura tra chiamate dello stesso batch non è impattato perché quelle chiamate confliggono e restano sequenziali.
- **Tool marcato safe ma non goroutine-safe**: mitigato dall'audit con `-race` della fase 1.
- **Latenza steering** fino al tool più lento del segmento (§4.6).

## 8. Decisioni aperte

1. Scritture parallele su path diversi in v1 (raccomandato, come da requisito della bozza) oppure `write_file`/`edit_file`/`append_file` sempre barriera (più prudente, zero modifiche al planner)?
2. Default di `max_parallel_tool_calls`: deciso `4` (non legato a `runtime.NumCPU()`).
3. Steering: attendere il segmento (v1, raccomandato) o in futuro non avviare i membri ancora in coda al semaforo?
