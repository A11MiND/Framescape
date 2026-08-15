import { useEffect, useRef, useState } from 'react'

// §19.2: "SSE 更新的数字变化用 200ms 计数动画，不做生硬跳变" — applied
// wherever a credit number can change under the user (rail balance, the
// composer's live estimate, the preview gate's cost comparison) rather than
// only where SSE happens to be the trigger.
export default function AnimatedNumber({ value, className }: { value: number; className?: string }) {
  const [display, setDisplay] = useState(value)
  const fromRef = useRef(value)
  const rafRef = useRef<number | undefined>(undefined)

  useEffect(() => {
    const from = fromRef.current
    const to = value
    if (from === to) return
    const start = performance.now()
    const duration = 200

    const tick = (now: number) => {
      const t = Math.min(1, (now - start) / duration)
      setDisplay(Math.round(from + (to - from) * t))
      if (t < 1) {
        rafRef.current = requestAnimationFrame(tick)
      } else {
        fromRef.current = to
      }
    }
    rafRef.current = requestAnimationFrame(tick)
    return () => {
      if (rafRef.current) cancelAnimationFrame(rafRef.current)
    }
  }, [value])

  return <span className={className}>{display}</span>
}
