import { Component, type ErrorInfo, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from '../ui'

function Fallback({ onRetry }: { onRetry: () => void }) {
  const { t } = useTranslation('codes')
  return <ErrorState message={t('unknown')} onRetry={onRetry} />
}

/** Keeps a crashing page from taking the navigation down with it. */
interface Props {
  children: ReactNode
  /** A new value (the route) clears a previous failure. */
  resetKey?: string
}

export class ErrorBoundary extends Component<Props, { failed: boolean; key?: string }> {
  state = { failed: false, key: this.props.resetKey }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  static getDerivedStateFromProps(props: Props, state: { failed: boolean; key?: string }) {
    return props.resetKey !== state.key ? { failed: false, key: props.resetKey } : null
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('page crashed', error, info.componentStack)
  }

  render() {
    if (this.state.failed) return <Fallback onRetry={() => this.setState({ failed: false })} />
    return this.props.children
  }
}
