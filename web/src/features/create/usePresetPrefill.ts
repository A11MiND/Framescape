import { useEffect, useRef } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useToast } from '../../components/Toast'

/**
 * "Apply to creation" from the preset library adds those presets to the
 * draft's own and says which were added; the rest of the draft is kept.
 */
export function usePresetPrefill(chosen: string[], set: (ids: string[]) => void) {
  const { t } = useTranslation('create')
  const toast = useToast()
  const location = useLocation()
  const navigate = useNavigate()
  // Effects can run twice for one navigation; each is applied once.
  const handled = useRef<unknown>(null)
  useEffect(() => {
    if (handled.current === location.state) return
    const p = (location.state as { prefillPresets?: { ids: string[]; names: string[] } } | null)?.prefillPresets
    if (!p?.ids.length) return
    handled.current = location.state
    set([...chosen, ...p.ids.filter((id) => !chosen.includes(id))])
    toast(t('presets.added', { names: p.names.join(t('ui:listSeparator')) }))
    navigate('.', { replace: true, state: {} })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [location.state])
}
