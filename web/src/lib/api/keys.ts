// Query keys, one factory per resource, so cache updates from the event
// stream and mutations target the same entries the pages read.
export const keys = {
  me: ['me'] as const,
  capabilities: ['capabilities'] as const,
  characters: ['characters'] as const,
  presets: ['presets'] as const,
  estimate: (r: unknown) => ['estimate', r] as const,
  preview: (r: unknown) => ['preview', r] as const,
  projects: {
    all: ['projects'] as const,
    list: () => ['projects', 'list'] as const,
    detail: (id: string) => ['projects', 'detail', id] as const,
  },
  jobs: {
    all: ['jobs'] as const,
    summary: (projectId?: string) => ['jobs', 'summary', projectId ?? null] as const,
    list: (filter: Record<string, unknown>) => ['jobs', 'list', filter] as const,
    detail: (id: string) => ['jobs', 'detail', id] as const,
  },
  assets: {
    all: ['assets'] as const,
    detail: (id: string) => ['assets', 'detail', id] as const,
  },
  credits: {
    all: ['credits'] as const,
  },
  comics: {
    all: ['comics'] as const,
    detail: (id: string) => ['comics', 'detail', id] as const,
  },
}
