/** A comic editor failure the UI translates by code (comic:error.<code>). */
export class ComicError extends Error {
  code: string
  params: Record<string, string | number>
  constructor(code: string, params: Record<string, string | number> = {}) {
    super(code)
    this.code = code
    this.params = params
  }
}
