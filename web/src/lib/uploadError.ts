export type UploadStage = 'prepare' | 'transfer' | 'confirm'

/** Keeps upload phase and cause without displaying storage URLs or server details. */
export class UploadError extends Error {
  readonly stage: UploadStage
  override readonly cause: unknown
  constructor(stage: UploadStage, cause: unknown) {
    super(`upload_${stage}`)
    this.stage = stage
    this.cause = cause
  }
}

export async function uploadStage<T>(stage: UploadStage, action: () => Promise<T>): Promise<T> {
  try {
    return await action()
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === 'AbortError') throw cause
    throw new UploadError(stage, cause)
  }
}
