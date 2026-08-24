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

// startedAt (a real timestamp, e.g. the job's earliest node.started_at) is
// optional — when given, elapsed is computed from real wall-clock time
// against it every tick, so navigating away from JobDetail and back doesn't
// reset the clock to 0 (found live: a job showing "已用时 11s" right after
// being reopened, when the underlying task had actually been running much
// longer). Falls back to counting up from mount only when no real timestamp
// is available yet (e.g. the very first tick before any node has started).
export default function GenerationProgress({ kind, startedAt }: { kind: 'image' | 'video'; startedAt?: string | null }) {
  const { t } = useTranslation()
  const startedAtMs = startedAt ? new Date(startedAt).getTime() : null
  const [mountElapsed, setMountElapsed] = useState(0)
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    const timer = setInterval(() => {
      setMountElapsed((e) => e + 1)
      setNow(Date.now())
    }, 1000)
    return () => clearInterval(timer)
  }, [])

  const elapsed = startedAtMs != null ? Math.max(0, Math.floor((now - startedAtMs) / 1000)) : mountElapsed

  function formatElapsed(seconds: number): string {
    if (seconds < 60) return `${seconds}s`
    return t('generationProgress.minSec', { min: Math.floor(seconds / 60), sec: seconds % 60 })
  }

  const stages = STAGE_KEYS[kind]
  const currentIndex = stages.reduce((idx, stage, i) => (elapsed >= stage.at ? i : idx), 0)
  // Past the happy-path window, the checklist just sits pinned on its last
  // stage with no explanation — often because a step actually failed once
  // and Aether is silently retrying it (retry is task-level and invisible
  // to the frontend, see jobsvc's task templates). Surfacing that honestly
  // beats leaving a checklist that looks frozen.
  const lastStageAt = stages[stages.length - 1].at
  const stillWorking = elapsed > lastStageAt + Math.max(15, lastStageAt * 0.2)

  return (
    <div className="flex flex-col items-center gap-4 text-zinc-400">
      <div className="flex items-center gap-3">
        <div className="flex h-5 items-center gap-1">
          {[0, 1, 2, 3, 4].map((i) => (
            <span
              key={i}
              className="animate-bar-wave h-full w-1 rounded-full bg-violet-500"
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
                done ? 'text-emerald-400' : active ? 'text-violet-400' : 'text-zinc-600'
              }`}
            >
              <span className="w-4 text-center">{done ? '✓' : active ? '⟳' : '○'}</span>
              <span className={active ? 'animate-pulse' : ''}>{t(`generationProgress.stage.${kind}.${stage.key}`)}</span>
            </li>
          )
        })}
      </ul>
      {stillWorking && <p className="text-center text-xs text-amber-500">{t('generationProgress.stillWorking')}</p>}
    </div>
  )
}
