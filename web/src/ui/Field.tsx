import {
  createContext,
  forwardRef,
  useContext,
  useId,
  useLayoutEffect,
  useRef,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from 'react'
import { useTranslation } from 'react-i18next'
import { CircleAlert } from 'lucide-react'
import { cn } from './cn'

interface FieldState {
  id: string
  describedBy?: string
  invalid: boolean
  required: boolean
}

const FieldContext = createContext<FieldState | null>(null)

export interface FieldProps {
  label: ReactNode
  help?: ReactNode
  /** Shown after the help text, never in place of it. */
  error?: ReactNode
  required?: boolean
  optional?: boolean
  className?: string
  children: ReactNode
}

/** A labelled control with persistent label, help text and inline error. */
export function Field({ label, help, error, required = false, optional = false, className, children }: FieldProps) {
  const { t } = useTranslation('ui')
  const id = useId()
  const helpId = help ? `${id}-help` : undefined
  const errorId = error ? `${id}-error` : undefined
  const describedBy = [helpId, errorId].filter(Boolean).join(' ') || undefined
  return (
    <FieldContext.Provider value={{ id, describedBy, invalid: Boolean(error), required }}>
      <div className={cn('flex flex-col gap-1.5', className)}>
        <label htmlFor={id} className="text-label font-medium text-fg">
          {label}
          {required && (
            <span aria-hidden className="ml-0.5 text-danger-fg">
              *
            </span>
          )}
          {optional && <span className="ml-1 font-normal text-fg-muted">{t('field.optional')}</span>}
        </label>
        {children}
        {help && (
          <p id={helpId} className="text-caption text-fg-muted">
            {help}
          </p>
        )}
        {error && (
          <p id={errorId} role="alert" className="flex items-start gap-1.5 text-caption text-danger-fg">
            <CircleAlert aria-hidden className="mt-0.5 size-3.5 shrink-0 text-danger-icon" />
            <span>{error}</span>
          </p>
        )}
      </div>
    </FieldContext.Provider>
  )
}

function useField() {
  return useContext(FieldContext)
}

// Width is left to the caller for selects (inline filters are content
// width); inputs and textareas fill their container.
// Only text controls: a <select> always matches :read-only.
const readOnlyText = 'read-only:bg-surface-2 read-only:text-fg-muted read-only:focus-visible:ring-0'

const controlBase =
  'rounded-card border bg-surface text-body text-fg placeholder:text-fg-muted transition-colors ' +
  'hover:border-fg-muted focus-visible:border-primary focus-visible:outline-none focus-visible:ring-3 focus-visible:ring-primary/20 ' +
  'disabled:cursor-not-allowed disabled:bg-surface-2 disabled:opacity-70'

function controlProps(field: FieldState | null, props: { id?: string; 'aria-describedby'?: string; required?: boolean }) {
  return {
    id: props.id ?? field?.id,
    'aria-describedby': props['aria-describedby'] ?? field?.describedBy,
    'aria-invalid': field?.invalid || undefined,
    'aria-required': field?.required || props.required || undefined,
  }
}

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input({ className, ...rest }, ref) {
  const field = useField()
  return (
    <input
      ref={ref}
      {...rest}
      {...controlProps(field, rest)}
      className={cn(controlBase, readOnlyText, 'h-10 w-full px-3', field?.invalid ? 'border-danger' : 'border-border-control', className)}
    />
  )
})

export const Select = forwardRef<HTMLSelectElement, SelectHTMLAttributes<HTMLSelectElement>>(function Select(
  { className, children, ...rest },
  ref,
) {
  const field = useField()
  return (
    <select
      ref={ref}
      {...rest}
      {...controlProps(field, rest)}
      className={cn(controlBase, 'h-10 px-3 pr-8', field ? 'w-full' : 'max-w-full', field?.invalid ? 'border-danger' : 'border-border-control', className)}
    >
      {children}
    </select>
  )
})

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  /** Shows a live character counter against this limit. */
  maxChars?: number
  autoGrow?: boolean
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea(
  { className, maxChars, autoGrow = true, value, ...rest },
  forwarded,
) {
  const field = useField()
  const inner = useRef<HTMLTextAreaElement | null>(null)
  const length = typeof value === 'string' ? [...value].length : 0
  useLayoutEffect(() => {
    const el = inner.current
    if (!autoGrow || !el) return
    el.style.height = 'auto'
    el.style.height = `${Math.min(Math.max(el.scrollHeight, 96), 240)}px`
  }, [value, autoGrow])
  const over = maxChars !== undefined && length > maxChars
  const near = maxChars !== undefined && !over && length >= maxChars * 0.9
  return (
    <div className="relative">
      <textarea
        ref={(el) => {
          inner.current = el
          if (typeof forwarded === 'function') forwarded(el)
          else if (forwarded) forwarded.current = el
        }}
        value={value}
        {...rest}
        {...controlProps(field, rest)}
        className={cn(
          controlBase,
          readOnlyText,
          'min-h-24 w-full resize-y px-3 py-2',
          maxChars !== undefined && 'pb-7',
          field?.invalid || over ? 'border-danger' : 'border-border-control',
          className,
        )}
      />
      {maxChars !== undefined && (
        <span
          aria-live="polite"
          className={cn(
            'pointer-events-none absolute right-3 bottom-2 text-caption tabular-nums',
            over ? 'text-danger-fg' : near ? 'text-warning-fg' : 'text-fg-muted',
          )}
        >
          {length} / {maxChars}
        </span>
      )}
    </div>
  )
})
