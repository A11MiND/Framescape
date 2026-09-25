export const JOB_STATUSES = ['queued', 'running', 'awaiting_review', 'succeeded', 'partial', 'failed', 'cancelling', 'cancelled'] as const

export type JobStatus = (typeof JOB_STATUSES)[number]

export function isJobStatus(s: string): s is JobStatus {
  return (JOB_STATUSES as readonly string[]).includes(s)
}
