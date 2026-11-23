/**
 * Parsing helpers for the per-model sampling fields.
 *
 * Empty fields inherit the matching agents.defaults value. The backend expects
 * an explicit 0 for max_tokens/context_window and null for temperature to clear
 * a per-model override.
 */

export interface ModelSamplingForm {
  maxTokens: string
  temperature: string
  contextWindow: string
}

export interface ModelSamplingPayload {
  max_tokens: number
  temperature: number | null
  context_window: number
}

export type ModelSamplingField = keyof ModelSamplingForm
export type ModelSamplingErrorReason = "number" | "integer" | "negative"

export type ModelSamplingValidation =
  | { ok: true; payload: ModelSamplingPayload }
  | {
      ok: false
      field: ModelSamplingField
      reason: ModelSamplingErrorReason
    }

type ParsedField =
  | { ok: true; value: number | null }
  | { ok: false; reason: ModelSamplingErrorReason }

function parseOptionalNumber(raw: string, integer: boolean): ParsedField {
  const trimmed = raw.trim()
  if (!trimmed) return { ok: true, value: null }

  const value = Number(trimmed)
  if (!Number.isFinite(value)) return { ok: false, reason: "number" }
  if (value < 0) return { ok: false, reason: "negative" }
  if (integer && !Number.isInteger(value)) {
    return { ok: false, reason: "integer" }
  }
  return { ok: true, value }
}

export function validateModelSampling(
  form: ModelSamplingForm,
): ModelSamplingValidation {
  const maxTokens = parseOptionalNumber(form.maxTokens, true)
  if (!maxTokens.ok) {
    return { ok: false, field: "maxTokens", reason: maxTokens.reason }
  }

  const temperature = parseOptionalNumber(form.temperature, false)
  if (!temperature.ok) {
    return { ok: false, field: "temperature", reason: temperature.reason }
  }

  const contextWindow = parseOptionalNumber(form.contextWindow, true)
  if (!contextWindow.ok) {
    return { ok: false, field: "contextWindow", reason: contextWindow.reason }
  }

  return {
    ok: true,
    payload: {
      max_tokens: maxTokens.value ?? 0,
      temperature: temperature.value,
      context_window: contextWindow.value ?? 0,
    },
  }
}
