import { createContext, useContext, useEffect, useRef } from 'react'
import type { StreamEvent } from './parser'

export type StreamStatus = 'idle' | 'connecting' | 'open' | 'offline'

export interface StreamState {
  status: StreamStatus
  /** When the last frame (event or heartbeat) arrived. */
  lastSyncAt: Date | null
  subscribe: (listener: (e: StreamEvent) => void) => () => void
}

export const StreamContext = createContext<StreamState | null>(null)

export function useStream(): StreamState {
  const ctx = useContext(StreamContext)
  if (!ctx) throw new Error('useStream must be used within StreamProvider')
  return ctx
}

/** Calls `listener` for every stream event while mounted. */
export function useStreamEvents(listener: (e: StreamEvent) => void) {
  const { subscribe } = useStream()
  const latest = useRef(listener)
  useEffect(() => {
    latest.current = listener
  })
  useEffect(() => subscribe((e) => latest.current(e)), [subscribe])
}
