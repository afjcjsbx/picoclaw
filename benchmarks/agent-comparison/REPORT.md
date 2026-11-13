# PicoClaw vs Hermes — benchmark su use case ordinari

**Data:** 10 novembre 2026  
**Modello:** `glm-5.3-flash` (unico modello usato)  
**Provider:** OpenCode Go (`https://opencode.ai/inference/go`)  
**PicoClaw:** `main` @ `c15da02a`, build Go 1.27.3 (`-tags goolm,stdjson`)  
**Hermes:** v0.23.1, Python 3.14.3, installazione da tarball di release (`hermes --version` riporta `vunknown` perché il tarball non contiene metadati git)  
**Host:** Apple M1 Max, macOS 27.3, 32 GiB RAM  
**Run analizzate:** `full-2026-11-10` e `full-2026-11-10-v2` (2 run per use case, 14 run per agente)

---

## 1. Sintesi esecutiva

- **Correttezza:** sostanziale pareggio. PicoClaw 13/14, Hermes 13/14. I fallimenti però sono diversi: PicoClaw ha sbagliato una risposta web perché si è fermato al preview troncato senza leggere l'artifact completo; Hermes ha **cancellato un file utente preesistente** in un run su due del task `feature-add`.
- **Tempo end-to-end (CLI a freddo):** PicoClaw è **~33% più veloce** (16,2 s vs 24,3 s medi). Il vantaggio però è quasi tutto nell'orchestrazione: Hermes paga 6–13 s di gateway/startup per invocazione; il suo tempo LLM è leggermente **inferiore** a quello di PicoClaw (14,0 s vs 15,6 s).
- **Token:** PicoClaw usa **~31% meno token** (28,6k vs 41,3k medi) e costa ~30% meno a listino ($0,0020 vs $0,0029 per task). Il vantaggio è netto su bug-fix (−54%), refactor (−50%), repo-QA (−55%).
- **Passi:** Hermes è più "deciso": 5,0 step e 5,4 tool call medi, contro 6,1 e 6,7 di PicoClaw. PicoClaw fa più passi soprattutto per attrito del tool layer, non per il modello.
- **Dove PicoClaw è migliore:** latenza a freddo, token/costo, confinamento al workspace, edit paralleli, preservazione dei dati.
- **Dove Hermes è migliore:** numero di passi, estrazione web (una chiamata invece di fetch+artifact), robustezza del tool layer, `read_file` con limiti usato bene.
- **Priorità per PicoClaw:** correggere i falsi positivi del safety guard di `exec` (8 comandi bloccati in 2 run su `log-triage`), permettere a `search_files` di leggere i propri artifact, migliorare l'ergonomia di `write_file` su file esistenti, e rafforzare la verifica delle fonti web troncate.

---

## 2. Metodo

### 2.1 Use case

Sette task ordinari, ognuno con workspace isolato e verifica deterministica:

| ID | Cosa misura | Esito verificato |
|---|---|---|
| `bug-fix` | debug + fix di un test Python che fallisce | `python3 -m unittest` passa, test non modificati, rounding corretto |
| `data-transform` | script di aggregazione CSV → JSON | valori esatti di totali, regioni e top product |
| `feature-add` | feature multi-file su CLI todo (priorità) | comportamento CLI, retrocompatibilità JSON, file legacy preservati |
| `log-triage` | analisi di un log applicativo | conteggi esatti per tipo di errore, tabella e root cause |
| `refactor` | rinomina di una funzione in un package | nessun riferimento residuo, test passanti |
| `repo-qa` | lettura e Q&A su un piccolo servizio | 4 risposte corrette, sorgenti non modificati |
| `web-research` | ricerca web e brief | versione Go stabile corrente (1.27.3) e data 2026-10-19 |

### 2.2 Configurazione comune

- Stesso modello e provider per entrambi, via un proxy locale che inoltra a OpenCode Go e registra richieste, risposte, usage e latenze. Il proxy aggiunge `x-opencode-session` quando il client non lo invia (richiesto da OpenCode Go).
- `temperature: 0`, `reasoning_effort: minimal`, massimo 40 step/turni, timeout 480 s (300 s per il task web).
- Stessi workspace di partenza, copiati per ogni run; nessuna memoria persistente, nessun subagent, nessuna skill, nessun MCP/cron/todo.
- Toolset ridotto e confrontabile: terminale/esecuzione, file (read/write/edit/search), web.
- PicoClaw: configurazione `opencode-go` con `api_base` sul proxy; Hermes: provider custom OpenAI-compatible con `api_mode: chat_completions`, `platform_toolsets.cli: [terminal, file, web]`.
- Hermes viene invocato con `hermes chat --yolo -q`, quindi paga l'avvio del gateway per ogni run; PicoClaw gira in-process.
- Nota installazione: l'installer ufficiale di Hermes non è riuscito a completare il checkout Git (errore HTTP 400 sul fetch lazy dei blob); l'istanza usata è stata installata dal tarball della release v0.23.1 con `uv sync`. Il codice è quello della release, ma `hermes --version` riporta `vunknown` per assenza di metadati git.

### 2.3 Metriche

- **Wall:** tempo totale osservato dal processo, inclusi startup e teardown.
- **LLM:** somma delle latenze delle chiamate al modello (dal proxy).
- **Overhead:** `wall − LLM` (gateway, startup, orchestrazione).
- **Step:** numero di cicli richiesta/risposta al modello.
- **Tool call:** numero di invocazioni di tool (una risposta può contenerne più in parallelo).
- **Token:** somma di `prompt_tokens + completion_tokens` su tutte le chiamate; i token di prompt in cache sono riportati a parte. Il costo è il `list_cost` restituito da OpenCode Go, non il costo effettivo dell'abbonamento Go.
- **Pass/score:** verifica deterministica post-run; lo score è la frazione di check superati.

---

## 3. Risultati aggregati (14 run per agente)

| Agente | Pass | Score | Wall medio | LLM medio | Overhead medio | Step medi | Tool call medi | Token medi | Costo lista medio |
|:--|--:|--:|--:|--:|--:|--:|--:|--:|--:|
| **PicoClaw** | **13/14** | 0,980 | **16,2 s** | 15,6 s | **0,7 s** | 6,1 | 6,7 | **28.569** | **$0,00202** |
| **Hermes** | **13/14** | 0,986 | 24,3 s | **14,0 s** | 10,3 s | **5,0** | **5,4** | 41.350 | $0,00290 |

Lettura corretta dei numeri:

- Il wall time premia PicoClaw perché Hermes, in questa modalità, avvia un gateway per ogni run. Con un gateway già caldo il vantaggio si ridurrebbe a circa 1–2 s di overhead: Hermes sarebbe probabilmente **leggermente più veloce** in wall, perché il suo tempo LLM è già inferiore.
- Il vantaggio di token di PicoClaw è invece strutturale in questi task: prompt di sistema molto più piccolo (~1,4k caratteri vs ~12k) e letture più mirate. Hermes recupera su `log-triage` quando usa `read_file` con `limit`, ma paga molto quando legge file interi.
- Il numero di passi premia Hermes, ma una parte del divario di PicoClaw è dovuta a errori/attriti del tool layer, non a ragionamento: 8 comandi `exec` bloccati nei due run di `log-triage`, 1 errore di `search_files` su un artifact, 1 rifiuto di `write_file` su file esistente.
- **Lo score non pesa la gravità.** Il fallimento di Hermes su `feature-add` (cancellazione di un file utente) vale 0,80; quello di PicoClaw su `web-research` (versione sbagliata) vale 0,71. Sono fallimenti di natura molto diversa: il primo è un rischio di perdita dati, il secondo un errore factuale recuperabile.

### 3.1 Per use case (media dei due run)

| Task | Agente | Pass | Wall | Step | Tool call | Token |
|:--|:--|:--:|--:|--:|--:|--:|
| bug-fix | PicoClaw | 2/2 | **12,3 s** | 5,0 | 5,0 | **19.976** |
| bug-fix | Hermes | 2/2 | 31,1 s | 6,0 | 5,0 | 43.025 |
| data-transform | PicoClaw | 2/2 | **11,5 s** | 5,0 | 4,0 | **26.021** |
| data-transform | Hermes | 2/2 | 21,5 s | **4,0** | **3,0** | 29.006 |
| feature-add | PicoClaw | **2/2** | **22,1 s** | 8,5 | 9,5 | **39.958** |
| feature-add | Hermes | 1/2 | 31,3 s | **5,5** | **6,5** | 43.272 |
| log-triage | PicoClaw | 2/2 | 27,4 s | 9,0 | 8,0 | **45.758** |
| log-triage | Hermes | 2/2 | **22,3 s** | **5,0** | **4,0** | 64.549 |
| refactor | PicoClaw | 2/2 | **13,3 s** | **5,0** | 9,0 | **21.756** |
| refactor | Hermes | 2/2 | 26,0 s | 6,0 | 9,5 | 43.530 |
| repo-qa | PicoClaw | 2/2 | **12,3 s** | **4,0** | **6,5** | **16.428** |
| repo-qa | Hermes | 2/2 | 22,3 s | 5,5 | 7,5 | 36.681 |
| web-research | PicoClaw | 1/2 | **14,8 s** | 6,0 | 5,0 | 30.087 |
| web-research | Hermes | **2/2** | 15,9 s | **3,0** | **2,5** | **29.384** |

### 3.2 Delta PicoClaw − Hermes

| Task | Wall | Step | Tool call | Token | Pass P / H |
|:--|--:|--:|--:|--:|:--:|
| bug-fix | −18,8 s (−60%) | −1,0 | 0,0 | −23.050 (−54%) | 2/2 / 2/2 |
| data-transform | −10,0 s (−47%) | +1,0 | +1,0 | −2.986 (−10%) | 2/2 / 2/2 |
| feature-add | −9,1 s (−29%) | +3,0 | +3,0 | −3.314 (−8%) | 2/2 / 1/2 |
| log-triage | +5,0 s (+22%) | +4,0 | +4,0 | −18.790 (−29%) | 2/2 / 2/2 |
| refactor | −12,7 s (−49%) | −1,0 | −0,5 | −21.774 (−50%) | 2/2 / 2/2 |
| repo-qa | −10,0 s (−45%) | −1,5 | −1,0 | −20.254 (−55%) | 2/2 / 2/2 |
| web-research | −1,1 s (−7%) | +3,0 | +2,5 | +702 (+2%) | 1/2 / 2/2 |

### 3.3 Varianza tra i due run (casi più instabili)

| Task | Agente | Run | Pass | Wall | Step | Token |
|:--|:--|:--|:--:|--:|--:|--:|
| feature-add | PicoClaw | v1 | ✅ | 23,9 s | 11 | 52.916 |
| feature-add | PicoClaw | v2 | ✅ | 20,4 s | 6 | 27.000 |
| feature-add | Hermes | v1 | ❌ | 26,3 s | 4 | 29.769 |
| feature-add | Hermes | v2 | ✅ | 36,2 s | 7 | 56.775 |
| bug-fix | Hermes | v1 | ✅ | 34,3 s | 7 | 51.653 |
| bug-fix | Hermes | v2 | ✅ | 27,9 s | 5 | 34.397 |
| log-triage | PicoClaw | v1 | ✅ | 27,2 s | 9 | 47.773 |
| log-triage | PicoClaw | v2 | ✅ | 27,5 s | 9 | 43.744 |
| log-triage | Hermes | v1 | ✅ | 21,2 s | 4 | 83.157 |
| log-triage | Hermes | v2 | ✅ | 23,4 s | 6 | 45.941 |
| web-research | PicoClaw | v1 | ✅ | 15,6 s | 6 | 31.824 |
| web-research | PicoClaw | v2 | ❌ | 14,1 s | 6 | 28.350 |
| web-research | Hermes | v1 | ✅ | 15,5 s | 3 | 30.866 |
| web-research | Hermes | v2 | ✅ | 16,4 s | 3 | 27.903 |

La tabella mostra perché due run non bastano per una classifica fine: `feature-add` PicoClaw passa da 11 a 6 step, Hermes da 4 a 7; `log-triage` Hermes da 4 a 6 step e da 83k a 46k token. Le medie nascondono questa variabilità.

---

## 4. Analisi per use case

### 4.1 `bug-fix` — PicoClaw netto

- Entrambi passano 2/2. PicoClaw: 5 step, 20k token, 12,3 s. Hermes: 6 step, 43k token, 31,1 s.
- PicoClaw legge subito `pricing.py` e `test_pricing.py`, capisce che il rounding richiesto è half-up e applica `(price_cents * percent + 50) // 100`. Un solo edit, test verdi.
- Hermes nel run v1 **indovina** il ceiling (`-(-price * percent // 100)`), esegue i test, fallisce, poi si corregge. Nel v2 vede subito il fallimento dei test e applica direttamente il fix corretto, chiudendo in 5 step. La variabilità è alta: 5 vs 7 step, 34k vs 52k token.
- Qui il vantaggio di token di PicoClaw è massimo (−54%) perché evita sia il prompt grande sia i cicli di retry.

### 4.2 `data-transform` — quasi pari, PicoClaw più veloce a freddo

- Entrambi 2/2. PicoClaw 5 step/26k/11,5 s; Hermes 4 step/29k/21,5 s.
- Approccio quasi identico: leggere il CSV, scrivere `aggregate.py`, eseguirlo. Hermes usa `head -5` per esplorare, PicoClaw legge il file intero; i token sono simili.
- Il tempo LLM di Hermes è leggermente migliore (9,2 s vs 11,1 s); il wall premia PicoClaw solo per l'assenza di gateway.

### 4.3 `feature-add` — il caso più importante: PicoClaw preserva i dati, Hermes no (in un run)

- PicoClaw 2/2, 22,1 s, 8,5 step, 40k token. Hermes 1/2, 31,3 s, 5,5 step, 43k token.
- **Run v1 Hermes:** per testare la retrocompatibilità sovrascrive `todos.json` con un file inventato, esegue i test e poi esegue `rm todos.json`. Il file originale con i due todo legacy viene perso. Il verifier fallisce due check: “legacy todos still list” e “legacy treated as medium”. È l'unico fallimento con perdita di dati del benchmark.
- **Run v2 Hermes:** ripete lo schema (`rm todos.json` durante i test) ma poi ricostruisce il file originale con `write_file` e passa. Il comportamento resta rischioso: se il run viene interrotto tra `rm` e il restore, i dati sono persi.
- **PicoClaw:** modifica temporaneamente `todos.json` per i test ma ripristina il contenuto originale; il file finale è identico a quello di partenza (il verifier non lo vede né changed né deleted). Nel v1 il ripristino passa da un `write_file` con `overwrite: true` dopo un rifiuto; nel v2 ripristina con un one-liner Python. In entrambi i run i dati legacy sopravvivono.
- Costo per PicoClaw: il retry di `write_file` e due comandi `git` in una directory non-git portano il v1 a 11 step (contro 6 del v2). È un problema di ergonomia del tool, non di giudizio.

### 4.4 `log-triage` — PicoClaw più economico, ma il safety guard gli costa 4 passi per run

- Entrambi 2/2. PicoClaw 27,4 s, 9 step, 46k token. Hermes 22,3 s, 5 step, 65k token.
- PicoClaw usa `grep`/`sed`/`awk` per contare gli errori. **Quattro comandi per run vengono bloccati** con `Command blocked by safety guard (path outside working dir)`, anche se operano solo su `app.log` nella workspace:
  - `grep ERROR app.log | sed -E 's/^[^ ]+ //; s/req=[0-9]+ //' | sort | uniq -c`
  - `grep ERROR app.log | awk '{$1=""; $2=""; sub(/^  /,""); ...}'`
  - nel v1 anche un comando con `"cwd"` uguale alla workspace viene rifiutato con lo stesso errore.
- Il modello insiste, prova `sed` più volte, poi passa a `cut`/`uniq` e riesce. I conteggi finali sono corretti (37/22/9/5), ma i 4 passi sprecati spiegano sia i 9 step sia il tempo LLM più alto di PicoClaw (26,8 s vs 15,5 s).
- Hermes nello stesso task usa `read_file` con `limit: 60` e un solo `grep`/`sed` (che il suo tool layer esegue senza blocchi), chiudendo in 5–6 step. Nel v1 però legge l'intero `app.log` (25k token di prompt in un colpo) e arriva a 83k token totali; nel v2, con `limit`, scende a 46k.
- È il caso che meglio riassume il trade-off: PicoClaw usa meno token, Hermes meno passi; se PicoClaw non bloccasse i comandi, probabilmente vincerebbe anche in passi e latenza LLM.

### 4.5 `refactor` — PicoClaw chiaramente più efficiente

- Entrambi 2/2. PicoClaw 5 step, 22k token, 13,3 s; Hermes 6 step, 44k token, 26,0 s.
- PicoClaw usa `search_files`, legge i due file chiave e applica **5 edit in parallelo in un solo step**. Hermes fa letture e patch più sequenziali (7 step nel v1, 5 nel v2).
- Nessun errore, nessun residuo `calc_avg`, test verdi. Qui PicoClaw vince su tutte le metriche tranne il numero di tool call (9 vs 9,5, praticamente pari).

### 4.6 `repo-qa` — PicoClaw più veloce e confinato

- Entrambi 2/2. PicoClaw 4 step, 16k token, 12,3 s; Hermes 5,5 step, 37k token, 22,3 s.
- PicoClaw legge i quattro file in parallelo e risponde correttamente su `reserve_item`, `InsufficientStock`, non-atomicità e formula del reorder point.
- Hermes usa `search_files` con path assoluto sulla root del repository e solo dopo `ls inventory/` e le letture corrette. È un comportamento “repo-aware” utile in un progetto reale, ma in un benchmark a workspace isolato gli fa esplorare directory estranee al task e aggiunge passi.
- PicoClaw resta confinato alla workspace: meno token, meno latenza.

### 4.7 `web-research` — Hermes più diretto, entrambi fragili sulle fonti

- Hermes 2/2, 3 step, 2,5 tool call, 29k token, 15,9 s. PicoClaw 1/2, 6 step, 5 tool call, 30k token, 14,8 s.
- **PicoClaw v1:** usa `web_fetch` con `maxChars: 2000/3000`, poi deve leggere l'artifact; `search_files` sull'artifact fallisce con `path escapes workspace`. Alla fine risponde correttamente 1.27.3.
- **PicoClaw v2:** usa `maxChars: 1500` sulla pagina release. Il risultato è troncato, ma il tool salva l'artifact completo (63 KB, con 1.27.0, 1.27.1, 1.27.2 e 1.27.3). Il modello però fa un grep solo su `go1.27.x (released …)`, vede 1.27.0 e conclude “Go 1.27.0 … no minor revisions”, che è **falso**. Non legge l'artifact alle righe 100+ dove c'è 1.27.3. È un fallimento di verifica/grounding del modello, non di capacità del tool: il dato c'era ed era raggiungibile.
- **Hermes:** `web_extract` restituisce la pagina completa in una chiamata e trova 1.27.3 in entrambi i run ufficiali. In un pre-run separato, però, anche Hermes ha risposto 1.27.0: la fragilità non è solo di PicoClaw.
- Nota: il task web è dipendente dalla rete e dalla pagina go.dev; i risultati 1/2 vs 2/2 non sono statisticamente solidi. Il pattern osservato è che PicoClaw, con preview piccoli, tende ad ancorarsi alla versione major senza leggere l'artifact completo.

---

## 5. Dove sbagliano e dove indovinano

### PicoClaw

**Indovina / fa bene:**
- `bug-fix`: legge i test prima di modificare e applica il rounding corretto al primo colpo (2/2).
- `refactor`: rinomina completa, nessun alias residuo, test verdi (2/2).
- `repo-qa`: risposte corrette su atomicità e formula, senza allucinazioni (2/2).
- `feature-add`: preserva i dati legacy in entrambi i run (2/2), anche quando il tool `write_file` lo ostacola.
- `log-triage`: non inventa i conteggi; dopo i blocchi del guard, li ricava dal testo grezzo e li riporta correttamente (2/2).

**Sbaglia / dove è fragile:**
- `web-research` v2: ancoraggio a Go 1.27.0 e affermazione “no minor revisions” basata su contenuto troncato. È l'unico fallimento ufficiale.
- Tool layer: 8 comandi `exec` bloccati nei due run di `log-triage`, 1 errore `search_files` su artifact, 1 rifiuto `write_file` su file esistente. Non sono errori di ragionamento, ma costano passi, token e latenza.
- In v1 `feature-add` esegue `git status`/`git diff` in una workspace non-git: due passi sprecati. Manca un controllo “sono in un repo?” prima di usare git.
- Non usa lo streaming nella CLI: con `streaming.enabled` sul modello, la richiesta parte comunque non-streaming perché manca la configurazione di streaming del canale (`pipeline_streaming.go` richiede anche `channelStreaming.Enabled`). Non altera i token totali, ma peggiora la latenza percepita.

### Hermes

**Indovina / fa bene:**
- `data-transform`: script corretto e diretto, 4 step, 3 tool call (2/2).
- `web-research`: `web_extract` completo, una chiamata, risposta corretta nei due run ufficiali (2/2).
- `refactor`, `repo-qa`, `bug-fix` (v2), `log-triage` (v2): pochi passi grazie a `read_file` con `limit`, `patch` e terminale.
- Tool layer robusto: nessun errore di esecuzione del tool nei run ufficiali; i comandi di test falliti sono stati gestiti e corretti.

**Sbaglia / dove è fragile:**
- `feature-add` v1: **cancella `todos.json`** e perde i dati legacy. È il fallimento più grave del benchmark. Nel v2 ripete `rm` ma ripristina il file; il pattern resta rischioso.
- `bug-fix` v1: indovina il ceiling prima di leggere l'intento dei test, fallisce e si corregge. Nel v2 va bene: alta varianza.
- `web-research` (pre-run): anche Hermes ha risposto 1.27.0 con eccessiva sicurezza. La fonte web non è garantita.
- `repo-qa`: cerca fuori dalla workspace (root del repo) prima di trovare i file del task; comportamento potenzialmente dispersivo e, in un benchmark, rischio di contaminazione tra task.
- Token: legge file interi quando potrebbe usare `limit`; nel v1 `log-triage` arriva a 83k token contro i 48k di PicoClaw.

---

## 6. Dove PicoClaw è migliore

1. **Latenza end-to-end su invocazioni CLI a freddo.** 16,2 s vs 24,3 s medi; overhead 0,7 s vs 10,3 s. Nessun gateway da avviare.
2. **Token e costo.** 28,6k vs 41,3k token medi (−31%), $0,0020 vs $0,0029 per task a listino (−30%). Il vantaggio è massimo su bug-fix, refactor e repo-QA.
3. **Confinamento al workspace.** I tool file/exec restano nella workspace; Hermes in `repo-qa` è andato sulla root del repository. Per un agente “personale” su dispositivi piccoli, il confinamento è un vantaggio di sicurezza e prevedibilità.
4. **Edit paralleli.** In `refactor` applica 5 edit in un solo step; in `repo-qa` legge 4 file in parallelo. Riduce i round-trip quando i tool funzionano.
5. **Preservazione dei dati.** In `feature-add` il file finale conserva i todo legacy in entrambi i run (con modifiche temporanee ripristinate); Hermes li ha persi in uno.
6. **Semplicità operativa.** Un solo processo, nessun gateway, nessuna configurazione di trasporto: più adatto a hardware limitato e ambienti effimeri.

---

## 7. Dove Hermes è migliore

1. **Step e tool call.** 5,0 step / 5,4 call vs 6,1 / 6,7. È più deciso e sfrutta meglio `read_file` con `limit`, `patch` e `web_extract`.
2. **Tempo LLM puro.** 14,0 s vs 15,6 s medi: a parità di infrastruttura calda, Hermes sarebbe probabilmente più veloce in wall. Il suo svantaggio è quasi tutto nel gateway a freddo.
3. **Estrazione web.** `web_search` + `web_extract` in una sola risposta, contenuto completo, 3 step contro 6. È il caso in cui il design di Hermes è chiaramente superiore.
4. **Robustezza del tool layer.** Nessun blocco di sicurezza sui comandi `sed`/`awk`, nessun problema a leggere i propri artifact, `patch`/`write_file` gestiscono meglio i file esistenti (anche se con il rischio di cancellazione visto sopra).
5. **Prompt più ricco.** Il system prompt (~12k caratteri) include più disciplina procedurale; quando la segue, riduce i passi. Il rovescio della medaglia è il costo in token e il rischio di iniziative distruttive.

---

## 8. Miglioramenti consigliati per PicoClaw (in ordine di priorità)

### P0 — Falsi positivi del safety guard di `exec`

**Evidenza:** 8 comandi bloccati in 2 run di `log-triage` (4 per run), sempre con `Command blocked by safety guard (path outside working dir)`, su comandi che leggono solo `app.log` nella workspace. Nel v1 è stato rifiutato anche un `cwd` uguale alla workspace.

**Causa probabile:** il parser dei path in `pkg/tools/shell.go` (`guardCommand`, `commandPathTextsFromMatch`) tratta parti di regex `sed`/`awk` (es. `s/^[^ ]+ //`, `sub(/^  /,"")`) come path assoluti/relativi e li risolve fuori dalla working directory. Il controllo `ValidatePathWithAllowPaths` sul `cwd` sembra inoltre rifiutare un path che è esattamente la workspace.

**Fix suggerito:**
- Ignorare i literal regex dopo `sed -e`/`sed -E` e dentro `sub(...)`, `gsub(...)`, `match(...)` di `awk`.
- Accettare sempre `cwd == workingDir` (normalizzato con `EvalSymlinks` e `filepath.Abs`).
- Aggiungere test di regressione con i comandi esatti osservati.
- Migliorare il messaggio: indicare quale token è stato interpretato come path, così il modello può correggere invece di ritentare.

**Impatto atteso:** `log-triage` dovrebbe scendere da 9 a ~5–6 step e da ~27 s a ~20 s, mantenendo i token bassi. È il singolo fix con il ROI maggiore.

### P0 — `search_files` non può leggere gli artifact del tool web

**Evidenza:** in `web-research` v1:
`failed to search ".../.artifacts/tool/web_fetch_688443222.txt": path escapes workspace`.

**Causa:** `pkg/tools/fs/search_files.go` valida il path con `filesystem.go` e considera `.artifacts/tool/...` fuori dalla workspace, anche se è un artifact creato da PicoClaw stesso.

**Fix suggerito:** includere `.artifacts` tra i path consentiti per la lettura/ricerca, oppure spostare gli artifact in una directory sotto `PICOCLAW_HOME` e restituire un handle leggibile. Aggiungere un test che cerchi dentro un artifact appena creato.

**Impatto:** elimina un passo perso e un errore fuorviante nel task web.

### P1 — Ergonomia di `write_file` su file esistenti

**Evidenza:** in `feature-add` v1:
`file: todos.json already exists. To add to it or change part of it without losing the current contents, use append_file or edit_file. Only set overwrite=true if you intend to replace the entire file.`
Il modello ritenta con `overwrite: true` e nel frattempo consuma un passo.

**Fix suggerito:** esporre `overwrite` nello schema del tool con una descrizione chiara; se il contenuto è identico, fare no-op; se il modello vuole solo aggiungere, suggerire automaticamente `append_file`/`edit_file`. In alternativa, restituire un errore strutturato con l'azione consigliata e il path, non solo testo.

**Impatto:** evita retry inutili su file di stato (`todos.json`, config, report) e riduce la varianza tra run (11 vs 6 step).

### P1 — Troncamento e verifica delle fonti web

**Evidenza:** in `web-research` v2 PicoClaw usa `maxChars: 1500`, riceve `"truncated": true` con l'artifact completo (63 KB), ma si ferma al preview e risponde 1.27.0 ignorando 1.27.3 presente nell'artifact.

**Fix suggerito:**
- Rafforzare il messaggio: “preview troncato; l'artifact completo è X KB e contiene il resto — leggilo con `read_file` offset/length prima di rispondere”.
- Nel system prompt: “non concludere mai da un risultato marcato `truncated`; leggi l'artifact o aumenta `maxChars`”.
- Scoraggiare `maxChars` molto piccoli per ricerche factual (es. minimo 4–8k) o mostrare un warning esplicito.
- Per fonti “latest version”, suggerire endpoint strutturati quando disponibili (es. `go.dev/VERSION?m=text`).

**Impatto:** evita risposte factually wrong e riduce i passi di recupero. Il tool ha già i dati; manca la disciplina di verifica.

### P1 — Disciplina procedurale nel system prompt

**Evidenza:** la varianza tra v1 e v2 su `feature-add` (11 vs 6 step; 53k vs 27k token) e `bug-fix` Hermes (7 vs 5 step) mostra che il comportamento dipende molto da cosa il modello decide di leggere prima di agire.

**Regole brevi da aggiungere:**
- “Leggi test/spec prima di modificare; esegui i test dopo ogni modifica.”
- “Non cancellare o sovrascrivere file preesistenti: usa copie temporanee per i test.”
- “Se un comando viene bloccato dal guard, leggi il motivo e cambia approccio (non ritentare lo stesso comando).”
- “Per i log, usa `read_file` con `length`/`offset` o `grep`; non leggere file interi se bastano poche righe.”
- “Controlla di essere in un repo git prima di usare `git`.”

**Impatto:** riduce passi sprecati e il rischio di azioni distruttive, senza aumentare molto il prompt.

### P2 — Streaming CLI

`pkg/agent/pipeline_streaming.go` richiede sia `model.streaming.enabled` sia lo streaming del canale. Nella configurazione CLI usata, lo streaming non è partito (`stream:false` nelle richieste). Percepito come lentezza anche quando i token totali sono bassi. Suggerimento: abilitare lo streaming di default per la CLI o documentare la doppia configurazione e mostrare un warning quando il modello lo abilita ma il canale no.

### P2 — Ridurre i comandi git inutili

In `feature-add` v1 PicoClaw esegue `git status`/`git diff` in una workspace non-git. Due passi e due output inutili. Aggiungere un check rapido (`git rev-parse --is-inside-work-tree`) o una regola nel prompt.

### P2 — Preferire i tool dedicati alle pipeline shell

Quando `exec` è bloccato o rischioso, il modello dovrebbe passare a `search_files`/`read_file` invece di insistere con `sed`/`awk`. Il prompt del tool `exec` potrebbe suggerire esplicitamente i tool alternativi per il text processing.

---

## 9. Limiti del benchmark

- **Due run per cella.** Con `temperature: 0` la varianza resta alta (es. `feature-add` PicoClaw 6 vs 11 step; Hermes 4 vs 7 step). I numeri vanno letti come indicativi, non come misure statisticamente robuste.
- **Prompt di sistema e tool diversi per design.** “Stessa configurazione” significa stesso modello/provider/toolset/limiti, non stessi prompt interni. Il confronto misura il prodotto, non solo il modello.
- **Hermes a freddo.** Ogni run avvia un gateway; in uso reale il gateway è spesso già caldo. Il tempo LLM separato permette di stimare il caso caldo, ma non è misurato direttamente.
- **Workspace annidate nel repo.** Hermes tratta la root del repository git come progetto e in `repo-qa` ha cercato fuori dalla workspace del task. Gli esiti verificati non sono stati contaminati, ma i passi di esplorazione possono risentirne.
- **Task web dipendente dalla rete.** La pagina go.dev e i risultati dei motori di ricerca possono cambiare; 1/2 vs 2/2 non è una misura stabile di accuratezza.
- **Token e costo.** I token sono la somma dell'usage per chiamata (inclusi i cached); il costo è il `list_cost` di OpenCode Go, non il costo effettivo dell'abbonamento. Le cache riducono il costo reale e sono indicate a parte (~72% di prompt in cache per PicoClaw, ~70% per Hermes).
- **Toolset ridotto.** Memoria, skill, subagent, todo, MCP e cron sono disabilitati per confrontare il core. Con le funzionalità complete i risultati possono cambiare, soprattutto su task lunghi.

---

## 10. Come riprodurre

```bash
cd benchmarks/agent-comparison

# 1) genera i workspace deterministici
python3 generate_tasks.py

# 2) build PicoClaw con i tag del progetto
(cd ../.. && go build -tags goolm,stdjson -o build/picoclaw-bench ./cmd/picoclaw)

# 3) provider (solo glm-5.3-flash)
export OPENCODE_GO_API_KEY="..."

# 4) esegui e analizza
python3 run_benchmark.py --label my-run --max-steps 40
python3 analyze.py my-run
python3 merge_runs.py my-run other-run
```

I dati grezzi (proxy JSONL, stdout/stderr, workspace, verifiche) sono in `benchmarks/agent-comparison/results/<label>/`. I riepiloghi sono in `summary.json`, `summary.md` e `analysis.md`; il merge è in `results/combined-*.md`.

> Nota: `run_benchmark.py` cerca Hermes in `~/hermes-src-0.23.1/.venv/bin/hermes`. Se usi un'altra installazione, aggiorna `HERMES_ROOT` all'inizio dello script.

---

## 11. Giudizio finale

- **Se devi eseguire un task breve su una macchina piccola o in CI, PicoClaw è la scelta migliore:** meno token, meno RAM/overhead, nessun gateway, latenza end-to-end più bassa, e nei nostri task ha protetto i dati meglio di Hermes.
- **Se ti serve il minor numero di passi possibile, una buona estrazione web e un tool layer che non ti blocca i comandi, Hermes è più maturo.** A gateway caldo è probabilmente anche più veloce in wall.
- **La priorità tecnica per PicoClaw è chiara:** sistemare il safety guard di `exec` e la gestione degli artifact. Sono difetti circoscritti, con evidenza ripetibile, e da soli spiegano gran parte del divario di step e latenza osservato.
