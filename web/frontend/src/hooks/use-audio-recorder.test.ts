import { type RenderHookResult, act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import type { RecordedAudio } from "@/features/chat/audio-recorder"

import { useAudioRecorder } from "./use-audio-recorder"

class FakeMediaRecorder {
  static readonly instances: FakeMediaRecorder[] = []
  static isTypeSupported(mime: string) {
    return mime === "audio/webm"
  }

  readonly mimeType: string
  readonly stream: MediaStream
  state: "inactive" | "recording" | "paused" = "inactive"
  ondataavailable: ((event: BlobEvent) => void) | null = null
  onstop: ((event: BlobEvent) => void) | null = null
  onerror: ((event: BlobEvent) => void) | null = null

  constructor(stream: MediaStream, options?: MediaRecorderOptions) {
    this.stream = stream
    this.mimeType = options?.mimeType ?? "audio/webm"
    FakeMediaRecorder.instances.push(this)
  }

  start() {
    this.state = "recording"
  }

  stop() {
    this.state = "inactive"
    this.onstop?.(new Event("stop") as BlobEvent)
  }

  emitData(data: Blob) {
    this.ondataavailable?.({ data } as BlobEvent)
  }

  fail() {
    this.onerror?.(new Event("error") as BlobEvent)
  }
}

function fakeStream() {
  const stop = vi.fn()
  const stream = {
    getTracks: () => [{ stop }],
  } as unknown as MediaStream
  return { stream, stop }
}

let mediaStream: MediaStream
let stopTrack: ReturnType<typeof vi.fn>
let getUserMedia: ReturnType<typeof vi.fn>

beforeEach(() => {
  FakeMediaRecorder.instances.length = 0
  const fake = fakeStream()
  mediaStream = fake.stream
  stopTrack = fake.stop
  getUserMedia = vi.fn().mockResolvedValue(mediaStream)
  vi.stubGlobal("MediaRecorder", FakeMediaRecorder)
  vi.stubGlobal("navigator", { mediaDevices: { getUserMedia } })
})

afterEach(() => {
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

async function startRecorder(
  result: RenderHookResult<
    ReturnType<typeof useAudioRecorder>,
    unknown
  >["result"],
) {
  await act(async () => {
    expect(await result.current.start()).toBe(true)
  })
  return FakeMediaRecorder.instances.at(-1)!
}

describe("useAudioRecorder", () => {
  it("starts recording and releases the stream on stop", async () => {
    const { result } = renderHook(() => useAudioRecorder())
    const recorder = await startRecorder(result)

    expect(result.current.isRecording).toBe(true)
    expect(recorder.state).toBe("recording")
    expect(getUserMedia).toHaveBeenCalledWith({ audio: true })

    let audio: RecordedAudio | null | undefined
    await act(async () => {
      recorder.emitData(new Blob(["voice"], { type: "audio/webm" }))
      audio = await result.current.stop()
    })

    expect(audio).toMatchObject({
      mimeType: "audio/webm",
      durationMs: expect.any(Number),
    })
    if (!audio) {
      throw new Error("expected recorded audio")
    }
    expect(audio.blob.size).toBeGreaterThan(0)
    expect(result.current.isRecording).toBe(false)
    expect(stopTrack).toHaveBeenCalledOnce()
  })

  it("returns null when stopped without audio chunks", async () => {
    const { result } = renderHook(() => useAudioRecorder())
    await startRecorder(result)

    let audio: RecordedAudio | null | undefined
    await act(async () => {
      audio = await result.current.stop()
    })

    expect(audio).toBeNull()
    expect(stopTrack).toHaveBeenCalledOnce()
  })

  it("cancels recording without returning audio", async () => {
    const { result } = renderHook(() => useAudioRecorder())
    const recorder = await startRecorder(result)
    recorder.emitData(new Blob(["voice"], { type: "audio/webm" }))

    await act(async () => {
      result.current.cancel()
      await Promise.resolve()
    })

    expect(result.current.isRecording).toBe(false)
    expect(recorder.state).toBe("inactive")
    expect(stopTrack).toHaveBeenCalledOnce()
  })

  it("resolves a pending stop with null on recorder errors", async () => {
    const { result } = renderHook(() => useAudioRecorder())
    const recorder = await startRecorder(result)
    let pending: Promise<RecordedAudio | null> | undefined

    await act(async () => {
      pending = result.current.stop()
      recorder.fail()
      expect(await pending).toBeNull()
    })

    expect(result.current.isRecording).toBe(false)
    expect(stopTrack).toHaveBeenCalledOnce()
  })

  it("propagates permission errors and resets starting state", async () => {
    getUserMedia.mockRejectedValue(
      new DOMException("microphone denied", "NotAllowedError"),
    )
    const { result } = renderHook(() => useAudioRecorder())

    await act(async () => {
      await expect(result.current.start()).rejects.toMatchObject({
        name: "NotAllowedError",
      })
    })

    expect(result.current.isStarting).toBe(false)
    expect(result.current.isRecording).toBe(false)
  })

  it("rejects unsupported environments", async () => {
    vi.stubGlobal("MediaRecorder", undefined)
    const { result } = renderHook(() => useAudioRecorder())

    await act(async () => {
      await expect(result.current.start()).rejects.toMatchObject({
        name: "NotSupportedError",
      })
    })

    expect(getUserMedia).not.toHaveBeenCalled()
  })

  it("stops the recorder and releases tracks on unmount", async () => {
    const { result, unmount } = renderHook(() => useAudioRecorder())
    const recorder = await startRecorder(result)

    unmount()

    expect(recorder.state).toBe("inactive")
    expect(stopTrack).toHaveBeenCalledOnce()
  })

  it("stops a stream that arrives after unmount", async () => {
    let resolveStream: ((stream: MediaStream) => void) | undefined
    getUserMedia.mockImplementation(
      () =>
        new Promise<MediaStream>((resolve) => {
          resolveStream = resolve
        }),
    )
    const { result, unmount } = renderHook(() => useAudioRecorder())
    let starting: Promise<boolean> | undefined

    act(() => {
      starting = result.current.start()
    })
    unmount()
    await act(async () => {
      resolveStream?.(mediaStream)
      expect(await starting).toBe(false)
    })

    expect(stopTrack).toHaveBeenCalledOnce()
    expect(FakeMediaRecorder.instances).toHaveLength(0)
  })

  it("tracks elapsed time while recording", async () => {
    vi.useFakeTimers()
    const { result } = renderHook(() => useAudioRecorder())
    await startRecorder(result)

    act(() => {
      vi.advanceTimersByTime(1000)
    })

    expect(result.current.elapsedMs).toBe(1000)
    act(() => {
      result.current.cancel()
    })
    expect(result.current.elapsedMs).toBe(0)
  })
})
