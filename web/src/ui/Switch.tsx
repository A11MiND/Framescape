import { cn } from './cn'

/** An on/off setting that takes effect immediately; the label names what it turns on. */
export function Switch({ checked, onChange, label, disabled, id }: { checked: boolean; onChange: (on: boolean) => void; label: string; disabled?: boolean; id?: string }) {
  return (
    <button
      id={id}
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn(
        'relative inline-flex h-6 w-11 shrink-0 items-center rounded-full border transition-colors disabled:cursor-not-allowed disabled:opacity-45',
        checked ? 'border-primary bg-primary' : 'border-border-control bg-surface-2',
      )}
    >
      <span className={cn('inline-block size-4.5 rounded-full bg-white shadow-sm transition-transform', checked ? 'translate-x-5.5' : 'translate-x-0.5')} />
    </button>
  )
}
