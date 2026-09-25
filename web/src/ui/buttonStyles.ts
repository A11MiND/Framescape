import { cn } from './cn'

export type ButtonVariant = 'primary' | 'secondary' | 'ghost' | 'danger' | 'danger-outline'
export type ButtonSize = 'sm' | 'md' | 'lg'

export const variants: Record<ButtonVariant, string> = {
  primary: 'bg-primary text-white hover:bg-primary-hover',
  secondary: 'bg-surface text-fg border border-border-control hover:bg-surface-2',
  ghost: 'text-primary-text hover:bg-primary-soft',
  danger: 'bg-danger text-white hover:bg-danger-hover',
  'danger-outline': 'bg-surface text-danger-fg border border-danger hover:bg-danger-soft',
}

export const sizes: Record<ButtonSize, string> = {
  sm: 'h-8 px-3 text-body gap-1.5',
  md: 'h-10 px-4 text-body gap-2',
  lg: 'h-12 px-5 text-body-lg font-semibold gap-2',
}

/** Button styling for elements that navigate (router links). */
export function buttonClasses(variant: ButtonVariant = 'secondary', size: ButtonSize = 'md', className?: string) {
  return cn(
    'inline-flex shrink-0 items-center justify-center rounded-card font-medium whitespace-nowrap transition-colors',
    variants[variant],
    sizes[size],
    className,
  )
}

