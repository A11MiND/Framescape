import { useState } from 'react'
import { CircleUserRound } from 'lucide-react'

export function AccountAvatar({ url, className }: { url?: string; className: string }) {
  const [failedURL, setFailedURL] = useState<string>()
  return url && failedURL !== url
    ? <img src={url} alt="" className={`${className} shrink-0 rounded-full object-cover`} onError={() => setFailedURL(url)} />
    : <CircleUserRound aria-hidden className={`${className} shrink-0 text-fg-muted`} />
}
