import "@testing-library/jest-dom/vitest"
import { cleanup } from "@testing-library/react"
import { afterEach } from "vitest"

// Vitest runs without globals, so Testing Library's automatic cleanup is not
// registered and has to be wired explicitly.
afterEach(() => {
  cleanup()
})
