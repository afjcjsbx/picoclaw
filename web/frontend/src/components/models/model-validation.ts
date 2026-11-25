/**
 * Real-time model field validation utilities.
 * All checks are pure frontend, no network required.
 *
 * Messages use i18n keys with interpolation params — callers must
 * translate them via t(key, params).
 */
import type { ModelProviderOption } from "@/api/models"

import {
  findClosestProvider,
  findProviderAlias,
  getKnownProviderKeys,
} from "./provider-registry"

export type ValidationLevel = "error" | "warning" | "success"

export interface FieldValidation {
  level: ValidationLevel
  messageKey: string
  messageParams?: Record<string, string>
  fix?: string
}

/**
 * Validate a model identifier string with optional provider context.
 * Returns validation result with optional one-click fix suggestion.
 */
export function validateModelField(
  input: string,
  selectedProvider?: string,
  backendOptions?: ModelProviderOption[],
): FieldValidation {
  const trimmed = input.trim()
  if (!trimmed) return { level: "success", messageKey: "" }
  const knownProviderKeys = getKnownProviderKeys(backendOptions)

  // Hard errors
  if (/\s/.test(trimmed)) {
    return {
      level: "error",
      messageKey: "models.validation.whitespace",
      fix: trimmed.replace(/\s+/g, "/"),
    }
  }
  if (trimmed.startsWith("/")) {
    return {
      level: "error",
      messageKey: "models.validation.leadingSlash",
      fix: trimmed.replace(/^\/+/, ""),
    }
  }
  if (trimmed.includes("//")) {
    return {
      level: "error",
      messageKey: "models.validation.consecutiveSlash",
      fix: trimmed.replace(/\/+/g, "/"),
    }
  }

  const slashIdx = trimmed.indexOf("/")
  if (slashIdx === -1) {
    // No provider prefix — when a provider is already selected,
    // the model ID is provider-local and needs no prefix.
    if (selectedProvider) {
      return {
        level: "success",
        messageKey: "models.validation.parsed",
        messageParams: { provider: selectedProvider, model: trimmed },
      }
    }
    return {
      level: "warning",
      messageKey: "models.validation.defaultToOpenAI",
      fix: `openai/${trimmed}`,
    }
  }

  const provider = trimmed.slice(0, slashIdx)
  const model = trimmed.slice(slashIdx + 1)
  if (!model) {
    return { level: "error", messageKey: "models.validation.emptyModel" }
  }

  if (!knownProviderKeys.has(provider)) {
    // Resolve aliases strictly: findProviderAlias returns undefined when the
    // prefix is not registered by the backend. getCanonicalProviderKey falls
    // back to the input instead, which made an arbitrary prefix look like a
    // resolved alias and surfaced a destructive one-click fix.
    const alias = findProviderAlias(provider, backendOptions)
    if (alias && alias !== provider) {
      return {
        level: "warning",
        messageKey: "models.validation.shouldUse",
        messageParams: { provider, alias },
        fix: `${alias}/${model}`,
      }
    }
    // A provider is already selected, so this field holds the canonical model
    // ID for that provider, and the UI documents that a slash inside the ID is
    // preserved as-is instead of being split again. Case-sensitive upstream
    // namespaces (ACME/gpt-4o-mini) must survive untouched: rewriting them to
    // lower case breaks routing at the provider.
    if (selectedProvider) {
      return {
        level: "success",
        messageKey: "models.validation.parsed",
        messageParams: { provider: selectedProvider, model: trimmed },
      }
    }
    // Typo check
    const closest = findClosestProvider(provider, backendOptions)
    if (closest) {
      return {
        level: "warning",
        messageKey: "models.validation.didYouMean",
        messageParams: { closest },
        fix: `${closest}/${model}`,
      }
    }
    return {
      level: "warning",
      messageKey: "models.validation.unknownProvider",
      messageParams: { provider },
    }
  }

  return {
    level: "success",
    messageKey: "models.validation.parsed",
    messageParams: { provider, model },
  }
}
