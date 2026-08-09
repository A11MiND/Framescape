import { useEffect, useState } from 'react'

// MiniMax gives no real progress signal before it finishes — a job is just
// Ready → Running → Succeeded, and image jobs alone take ~15-30s (video:
// minutes). A bare spinner reads as "frozen" well before that, so this fakes
// a plausible step-by-step checklist driven purely by elapsed time. Its job
// is to keep visibly ticking so the user trusts the tab is still alive, not
// to reflect what MiniMax is actually doing server-side.
const STAGES: Record<'image' | 'video', { label: string; at: number }[]> = {
  image: [
    { label: '起稿', at: 0 },
    { label: '勾勒轮廓', at: 3 },
    { label: '添加细节', at: 7 },
    { label: '上色', at: 11 },
    { label: '添加光影', at: 15 },
    { label: '检查细节', at: 20 },
    { label: '检查整体效果', at: 26 },
  ],
  video: [
    { label: '构思分镜', at: 0 },
    { label: '渲染画面', at: 15 },
    { label: '添加运动与转场', at: 40 },
    { label: '调色', at: 70 },
    { label: '合成音效', at: 100 },
    { label: '编码输出', at: 130 },
    { label: '最终质检', at: 165 },
  ],
}

function formatElapsed(seconds: number): string {
  if (seconds < 60) return `${seconds}s`
  return `${Math.floor(seconds / 60)}分${seconds % 60}秒`
}

export default function GenerationProgress({ kind }: { kind: 'image' | 'video' }) {
  const [elapsed, setElapsed] = useState(0)

  useEffect(() => {
    const timer = setInterval(() => setElapsed((e) => e + 1), 1000)
    return () => clearInterval(timer)
  }, [])

  const stages = STAGES[kind]
  const currentIndex = stages.reduce((idx, stage, i) => (elapsed >= stage.at ? i : idx), 0)

  return (
    <div className="flex flex-col items-center gap-4 text-zinc-400">
      <div className="flex items-center gap-2">
        <div className="h-5 w-5 animate-spin rounded-full border-2 border-sky-500 border-t-transparent" />
        <span className="text-sm text-zinc-300">生成中，请勿关闭页面…</span>
        <span className="font-mono text-xs text-zinc-500">已用时 {formatElapsed(elapsed)}</span>
      </div>
      <ul className="space-y-1.5 text-sm">
        {stages.map((stage, i) => {
          const done = i < currentIndex
          const active = i === currentIndex
          return (
            <li
              key={stage.label}
              className={`flex items-center gap-2 transition-colors ${
                done ? 'text-emerald-400' : active ? 'text-sky-400' : 'text-zinc-600'
              }`}
            >
              <span className="w-4 text-center">{done ? '✓' : active ? '⟳' : '○'}</span>
              <span className={active ? 'animate-pulse' : ''}>{stage.label}</span>
            </li>
          )
        })}
      </ul>
    </div>
  )
}
