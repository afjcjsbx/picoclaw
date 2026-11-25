import { atom } from "jotai"

// Bumped after a default-model change made outside useChatModels so every
// mounted model consumer reloads the model list instead of showing stale data.
export const modelsRevisionAtom = atom(0)
