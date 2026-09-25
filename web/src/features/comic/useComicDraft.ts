import { useCallback, useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError } from '../../lib/api/client'
import { comicsApi } from '../../lib/api/comics'
import { keys } from '../../lib/api/keys'
import { parseComicDocument, type ComicDocument, type ComicLayer, type SavedComic } from '../../lib/comicDocument'

/** The server copy changed since this draft was loaded or saved. */
export class ComicConflict extends Error {
  constructor() {
    super('version_conflict')
  }
}

const HISTORY_LIMIT = 40

/**
 * One comic draft being edited: the document with undo and redo, a local
 * backup kept on every change (per account), and saving to the account with
 * optimistic versions. A stale save throws ComicConflict and changes nothing.
 */
export function useComicDraft(userId: string | undefined, initial: () => ComicDocument) {
  const qc = useQueryClient()
  const [doc, setDoc] = useState(initial)
  const live = useRef(doc)
  const [history, setHistory] = useState<ComicDocument[]>([])
  const [future, setFuture] = useState<ComicDocument[]>([])
  const saved = useRef<{ id: string | null; version: number }>({ id: null, version: 0 })
  const [savedId, setSavedId] = useState<string | null>(null)
  const [savedAt, setSavedAt] = useState<Date | null>(null)
  const [savedDoc, setSavedDoc] = useState<ComicDocument | null>(null)
  const [backedUpAt, setBackedUpAt] = useState<Date | null>(null)
  const [backupFailed, setBackupFailed] = useState(false)
  const [recovery, setRecovery] = useState<SavedComic | null>(null)
  const storageKey = userId ? `aigc.comic-draft.${userId}` : null
  const keyRef = useRef(storageKey)
  keyRef.current = storageKey

  const backup = useCallback(() => {
    if (!keyRef.current) return
    localStorage.setItem(keyRef.current, JSON.stringify({ biz_id: saved.current.id ?? '', version: saved.current.version, document: live.current }))
    setBackedUpAt(new Date())
    setBackupFailed(false)
  }, [])

  // A backup left by an earlier visit is offered once, before it is overwritten.
  useEffect(() => {
    if (!storageKey) return
    try {
      const raw = localStorage.getItem(storageKey)
      if (raw) {
        const data = JSON.parse(raw) as SavedComic
        setRecovery({ ...data, document: parseComicDocument(data.document) })
      }
    } catch {
      // an unreadable backup is ignored; the account copy is unaffected
    }
  }, [storageKey])

  useEffect(() => {
    if (!storageKey || recovery) return
    const timer = setTimeout(() => {
      try {
        backup()
      } catch {
        setBackupFailed(true)
      }
    }, 400)
    return () => clearTimeout(timer)
  }, [doc, storageKey, recovery, backup])

  useEffect(() => {
    const handler = (event: BeforeUnloadEvent) => {
      try {
        backup()
      } catch {
        // the account copy is unaffected
      }
      if (live.current !== savedDoc && (live.current.brief || live.current.layers.length)) {
        event.preventDefault()
        event.returnValue = ''
      }
    }
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [backup, savedDoc])

  const change = useCallback((next: ComicDocument, undoable = true) => {
    // Capture the replaced document now: the updaters run after live moves on.
    const previous = live.current
    if (undoable) {
      setHistory((h) => [...h.slice(-(HISTORY_LIMIT - 1)), previous])
      setFuture([])
    }
    live.current = next
    setDoc(next)
  }, [])

  const undo = () => {
    const prior = history[history.length - 1]
    if (!prior) return
    const current = live.current
    setHistory((h) => h.slice(0, -1))
    setFuture((f) => [...f, current])
    change({ ...prior, pending: live.current.pending }, false)
  }
  const redo = () => {
    const next = future[future.length - 1]
    if (!next) return
    const current = live.current
    setFuture((f) => f.slice(0, -1))
    setHistory((h) => [...h, current])
    change({ ...next, pending: live.current.pending }, false)
  }

  const updateLayer = (layer: ComicLayer) => change({ ...live.current, layers: live.current.layers.map((l) => (l.id === layer.id ? layer : l)) })

  const markSaved = (result: SavedComic, value: ComicDocument) => {
    saved.current = { id: result.biz_id, version: result.version }
    setSavedId(result.biz_id)
    setSavedAt(new Date())
    setSavedDoc(value)
    try {
      backup()
    } catch {
      setBackupFailed(true)
    }
    qc.invalidateQueries({ queryKey: keys.comics.all })
  }

  /** Saves to the account; a stale version throws ComicConflict. */
  const persist = async (value: ComicDocument = live.current) => {
    try {
      const result = await comicsApi.save(saved.current.id, saved.current.version, value)
      markSaved(result, value)
      return result
    } catch (err) {
      if (err instanceof ApiError && err.code === 'version_conflict') throw new ComicConflict()
      throw err
    }
  }

  /** Replaces the newer server copy with this one. */
  const overwrite = async () => {
    if (!saved.current.id) return persist()
    const server = await comicsApi.get(saved.current.id)
    saved.current = { id: server.biz_id, version: server.version }
    return persist()
  }

  /** Saves this document as a separate draft and continues editing that one. */
  const saveCopy = async () => {
    const previous = saved.current
    saved.current = { id: null, version: 0 }
    try {
      return await persist()
    } catch (err) {
      saved.current = previous
      throw err
    }
  }

  /** Switches to another draft; the current document stays in the undo history. */
  const activate = (value: SavedComic, keepUndo = false) => {
    const parsed = parseComicDocument(value.document)
    saved.current = { id: value.biz_id || null, version: value.version }
    setSavedId(value.biz_id || null)
    setSavedAt(null)
    setSavedDoc(value.biz_id ? parsed : null)
    if (keepUndo) change(parsed)
    else {
      change(parsed, false)
      setHistory([])
      setFuture([])
    }
    setRecovery(null)
  }

  return {
    doc,
    live,
    change,
    updateLayer,
    undo,
    redo,
    canUndo: history.length > 0,
    canRedo: future.length > 0,
    savedId,
    savedAt,
    dirty: doc !== savedDoc,
    backedUpAt,
    backupFailed,
    recovery,
    dismissRecovery: () => setRecovery(null),
    persist,
    overwrite,
    saveCopy,
    activate,
    backup,
  }
}

export type ComicDraft = ReturnType<typeof useComicDraft>
