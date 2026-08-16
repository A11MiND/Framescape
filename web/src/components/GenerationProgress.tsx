import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

// MiniMax gives no real progress signal before it finishes — a job is just
// Ready → Running → Succeeded, and image jobs alone take ~15-30s (video:
// minutes). A bare spinner reads as "frozen" well before that, so this fakes
// a plausible step-by-step checklist driven purely by elapsed time. Its job
// is to keep visibly ticking so the user trusts the tab is still alive, not
// to reflect what MiniMax is actually doing server-side.
const STAGE_KEYS: Record<'image' | 'video', { key: string; at: number }[]> = {
  image: [
    { key: 'sketch', at: 0 },
    { key: 'outline', at: 3 },
    { key: 'detail', at: 7 },
    { key: 'color', at: 11 },
    { key: 'lighting', at: 15 },
    { key: 'checkDetail', at: 20 },
    { key: 'checkOverall', at: 26 },
  ],
  video: [
    { key: 'storyboard', at: 0 },
    { key: 'render', at: 15 },
    { key: 'motion', at: 40 },
    { key: 'colorGrade', at: 70 },
    { key: 'audio', at: 100 },
    { key: 'encode', at: 130 },
    { key: 'finalCheck', at: 165 },
  ],
}

export default function GenerationProgress({ kind }: { kind: 'image' | 'video' }) {
  const { t } = useTranslation()
  const [elapsed, setElapsed] = useState(0)

  useEffect(() => {
    const timer = setInterval(() => setElapsed((e) => e + 1), 1000)
    return () => clearInterval(timer)
  }, [])

  function formatElapsed(seconds: number): string {
    if (seconds < 60) return `${seconds}s`
    return t('generationProgress.minSec', { min: Math.floor(seconds / 60), sec: seconds % 60 })
  }

  const stages = STAGE_KEYS[kind]
  const currentIndex = stages.reduce((idx, stage, i) => (elapsed >= stage.at ? i : idx), 0)

  return (
    <div className="flex flex-col items-center gap-4 text-zinc-400">
      <div className="flex items-center gap-3">
        <div className="flex h-5 items-center gap-1">
          {[0, 1, 2, 3, 4].map((i) => (
            <span
              key={i}
              className="animate-bar-wave h-full w-1 rounded-full bg-sky-500"
              style={{ animationDelay: `${i * 0.12}s` }}
            />
          ))}
        </div>
        <span className="text-sm text-zinc-300">{t('generationProgress.generating')}</span>
        <span className="font-mono text-xs text-zinc-500">{t('generationProgress.elapsed', { time: formatElapsed(elapsed) })}</span>
      </div>
      <ul className="space-y-1.5 text-sm">
        {stages.map((stage, i) => {
          const done = i < currentIndex
          const active = i === currentIndex
          return (
            <li
              key={stage.key}
              className={`flex items-center gap-2 transition-colors ${
                done ? 'text-emerald-400' : active ? 'text-sky-400' : 'text-zinc-600'
              }`}
            >
              <span className="w-4 text-center">{done ? '✓' : active ? '⟳' : '○'}</span>
              <span className={active ? 'animate-pulse' : ''}>{t(`generationProgress.stage.${kind}.${stage.key}`)}</span>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
