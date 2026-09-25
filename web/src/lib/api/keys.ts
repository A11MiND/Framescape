// Query keys, one factory per resource, so cache updates from the event
// stream and mutations target the same entries the pages read.
export const keys = {
  me: ['me'] as const,
  capabilities: ['capabilities'] as const,
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
  credits: {
    all: ['credits'] as const,
  },
}
