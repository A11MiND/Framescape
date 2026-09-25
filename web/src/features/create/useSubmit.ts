import { useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { createApi, type CreateRequest } from '../../lib/api/create'
import { keys } from '../../lib/api/keys'
import { ApiError } from '../../lib/api/client'
import { errorText } from '../../lib/errorText'
import { useDebouncedValue } from '../../hooks/useDebouncedValue'
import { useToast } from '../../components/Toast'

export type SubmitNotice = null | 'priceChanged' | 'insufficient'

/**
 * Quotes a request (debounced) and submits it at the quoted price. A
 * double click reuses the idempotency key, so it never creates two jobs.
 */
export function useSubmit(request: CreateRequest | null, onCreated: (bizId: string) => void) {
  const { t } = useTranslation('create')
  const qc = useQueryClient()
  const toast = useToast()
  const [notice, setNotice] = useState<SubmitNotice>(null)
  const idem = useRef<{ body: string; key: string } | null>(null)
  const body = request ? JSON.stringify(request) : ''
  const debounced = useDebouncedValue(body, 300)
  const settled = debounced === body
  const estimate = useQuery({
    queryKey: keys.estimate(debounced),
    queryFn: ({ signal }) => createApi.estimate(JSON.parse(debounced) as CreateRequest, signal),
    enabled: Boolean(debounced),
    retry: false,
    staleTime: 30_000,
  })

  const submit = useMutation({
    mutationFn: () => {
      if (!request || !estimate.data) throw new Error('not ready')
      if (!idem.current || idem.current.body !== body) idem.current = { body, key: crypto.randomUUID() }
      return createApi.create({ ...request, quote_total: estimate.data.credits_total }, idem.current.key)
    },
    onSuccess: (res) => {
      idem.current = null
      setNotice(null)
      qc.invalidateQueries({ queryKey: keys.jobs.all })
      qc.invalidateQueries({ queryKey: keys.me })
      onCreated(res.biz_id)
    },
    onError: (err) => {
      if (err instanceof ApiError) idem.current = null
      if (err instanceof ApiError && err.code === 'price_changed') {
        setNotice('priceChanged')
        estimate.refetch()
      } else if (err instanceof ApiError && err.code === 'insufficient_credits') {
        setNotice('insufficient')
      } else {
        toast(errorText(t, err))
      }
    },
  })

  return {
    estimate: request ? estimate : null,
    /** True while the shown quote belongs to an older version of the request. */
    stale: !settled || estimate.isFetching,
    submit: () => {
      setNotice(null)
      submit.mutate()
    },
    submitting: submit.isPending,
    /** Why the current request cannot be quoted, localized. */
    estimateError: request && estimate.error ? errorText(t, estimate.error) : undefined,
    notice,
    canSubmit: Boolean(request && estimate.data && settled && !estimate.isFetching && !submit.isPending),
  }
}
