import { forwardRef, useState, type InputHTMLAttributes } from 'react'
import { useTranslation } from 'react-i18next'
import { Eye, EyeOff } from 'lucide-react'
import { Input } from './Field'

/** A password field whose content can be shown and hidden again. */
export const PasswordInput = forwardRef<HTMLInputElement, Omit<InputHTMLAttributes<HTMLInputElement>, 'type'>>(function PasswordInput({ className, ...rest }, ref) {
  const { t } = useTranslation('ui')
  const [shown, setShown] = useState(false)
  return (
    <div className="relative">
      <Input ref={ref} {...rest} type={shown ? 'text' : 'password'} className={`pr-10 ${className ?? ''}`} />
      <button
        type="button"
        aria-label={shown ? t('password.hide') : t('password.show')}
        aria-pressed={shown}
        onClick={() => setShown(!shown)}
        className="absolute top-1/2 right-1 inline-flex size-8 -translate-y-1/2 items-center justify-center rounded-[8px] text-fg-muted hover:bg-surface-2 hover:text-fg"
      >
        {shown ? <EyeOff aria-hidden className="size-4" /> : <Eye aria-hidden className="size-4" />}
      </button>
    </div>
  )
})
