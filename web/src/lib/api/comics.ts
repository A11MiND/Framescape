import { query, request } from './client'
import type { ComicDocument, ComicSummary, SavedComic } from '../comicDocument'

export const comicsApi = {
  list: (cursor?: string) => request<{ comics: ComicSummary[]; next_cursor?: string }>('GET', `/comics${query({ limit: 50, cursor })}`),
  get: (id: string) => request<SavedComic>('GET', `/comics/${id}`),
  save: (id: string | null, version: number, document: ComicDocument) =>
    request<SavedComic>(id ? 'PATCH' : 'POST', id ? `/comics/${id}` : '/comics', { version, document }),
  remove: (id: string) => request<void>('DELETE', `/comics/${id}`),
}
