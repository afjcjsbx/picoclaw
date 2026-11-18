import "@testing-library/jest-dom/vitest"
import { cleanup } from "@testing-library/react"
import { afterEach } from "vitest"

// Vitest runs without globals, so Testing Library's automatic cleanup is not
// registered and has to be wired explicitly.
afterEach(() => {
  cleanup()
})

function createMemoryStorage(): Storage {
  const entries = new Map<string, string>()

  return {
    get length() {
      return entries.size
    },
    clear() {
      entries.clear()
    },
    getItem(key: string) {
      return entries.has(key) ? (entries.get(key) ?? null) : null
    },
    key(index: number) {
      return Array.from(entries.keys())[index] ?? null
    },
    removeItem(key: string) {
      entries.delete(key)
    },
    setItem(key: string, value: string) {
      entries.set(String(key), String(value))
    },
  }
}

// Node 25 exposes a global localStorage that lacks the Storage methods unless
// it is started with a valid --localstorage-file, and that broken object wins
// over the jsdom implementation. Install a working in-memory storage when the
// available one is unusable so browser-storage tests stay deterministic.
if (typeof globalThis.localStorage?.clear !== "function") {
  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: createMemoryStorage(),
  })
}
