import { useCallback, useEffect, useRef, useState } from "react"

import {
  type RecordedAudio,
  isAudioRecordingSupported,
  normalizeAudioMimeType,
  pickAudioRecordingMimeType,
} from "@/features/chat/audio-recorder"

interface PendingStop {
  resolve: (audio: RecordedAudio | null) => void
}

export function useAudioRecorder() {
  const [isRecording, setIsRecording] = useState(false)
  const [isStarting, setIsStarting] = useState(false)
  const [elapsedMs, setElapsedMs] = useState(0)
  const recorderRef = useRef<MediaRecorder | null>(null)
  const streamRef = useRef<MediaStream | null>(null)
  const chunksRef = useRef<Blob[]>([])
  const startedAtRef = useRef(0)
  const discardRef = useRef(false)
  const pendingStopRef = useRef<PendingStop | null>(null)
  const mountedRef = useRef(true)

  const releaseStream = useCallback(() => {
    streamRef.current?.getTracks().forEach((track) => track.stop())
    streamRef.current = null
  }, [])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      discardRef.current = true
      const recorder = recorderRef.current
      if (recorder && recorder.state !== "inactive") {
        recorder.stop()
      }
      releaseStream()
    }
  }, [releaseStream])

  useEffect(() => {
    if (!isRecording) {
      setElapsedMs(0)
      return
    }

    const update = () => setElapsedMs(Date.now() - startedAtRef.current)
    update()
    const timer = window.setInterval(update, 250)
    return () => window.clearInterval(timer)
  }, [isRecording])

  const start = useCallback(async (): Promise<boolean> => {
    if (recorderRef.current) return false
    if (!isAudioRecordingSupported()) {
      throw new DOMException("unsupported", "NotSupportedError")
    }

    discardRef.current = false
    setIsStarting(true)
    let stream: MediaStream | null = null
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
      if (!mountedRef.current || discardRef.current) {
        stream.getTracks().forEach((track) => track.stop())
        return false
      }

      const mimeType = pickAudioRecordingMimeType()
      const recorder = mimeType
        ? new MediaRecorder(stream, { mimeType })
        : new MediaRecorder(stream)

      chunksRef.current = []
      recorder.ondataavailable = (event) => {
        if (event.data.size > 0) {
          chunksRef.current.push(event.data)
        }
      }
      recorder.onstop = () => {
        const durationMs = Math.max(0, Date.now() - startedAtRef.current)
        const chunks = chunksRef.current
        const discard = discardRef.current
        const pending = pendingStopRef.current

        chunksRef.current = []
        discardRef.current = false
        pendingStopRef.current = null
        recorderRef.current = null
        releaseStream()

        if (mountedRef.current) {
          setIsRecording(false)
          setElapsedMs(0)
        }

        if (!pending) return
        if (!mountedRef.current || discard || chunks.length === 0) {
          pending.resolve(null)
          return
        }

        const normalizedMimeType = normalizeAudioMimeType(
          recorder.mimeType || mimeType,
        )
        pending.resolve({
          blob: new Blob(chunks, { type: normalizedMimeType }),
          mimeType: normalizedMimeType,
          durationMs,
        })
      }
      recorder.onerror = () => {
        const pending = pendingStopRef.current
        pendingStopRef.current = null
        recorderRef.current = null
        releaseStream()
        if (mountedRef.current) {
          setIsRecording(false)
          setElapsedMs(0)
        }
        pending?.resolve(null)
      }

      recorder.start(1000)
      streamRef.current = stream
      recorderRef.current = recorder
      startedAtRef.current = Date.now()
      setIsRecording(true)
      return true
    } catch (error) {
      stream?.getTracks().forEach((track) => track.stop())
      throw error
    } finally {
      setIsStarting(false)
    }
  }, [releaseStream])

  const stop = useCallback((): Promise<RecordedAudio | null> => {
    const recorder = recorderRef.current
    if (!recorder || recorder.state === "inactive") {
      return Promise.resolve(null)
    }

    return new Promise((resolve) => {
      pendingStopRef.current = { resolve }
      try {
        recorder.stop()
      } catch {
        pendingStopRef.current = null
        recorderRef.current = null
        releaseStream()
        setIsRecording(false)
        resolve(null)
      }
    })
  }, [releaseStream])

  const cancel = useCallback(() => {
    discardRef.current = true
    void stop()
  }, [stop])

  return {
    isRecording,
    isStarting,
    elapsedMs,
    start,
    stop,
    cancel,
  }
}
