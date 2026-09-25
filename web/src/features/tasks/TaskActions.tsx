import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Ellipsis } from 'lucide-react'
import { Button, ConfirmDialog, Dialog, Field, IconButton, Input, Menu, type MenuItem } from '../../ui'
import { jobsApi, type JobListItem } from '../../lib/api/jobs'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { useToast } from '../../components/Toast'
import { ACTIVE_STATUSES, TERMINAL_STATUSES } from './filters'

type Pending = null | 'rename' | 'cancel' | 'delete'

/** Rename, cancel and delete a task, each confirmed with its target and effect. */
export function TaskActions({ job, title }: { job: JobListItem; title: string }) {
  const { t } = useTranslation('tasks')
  const qc = useQueryClient()
  const toast = useToast()
  const [pending, setPending] = useState<Pending>(null)
  const [name, setName] = useState(job.title)
  const refresh = () => qc.invalidateQueries({ queryKey: keys.jobs.all })

  const onError = (err: unknown) => toast(errorText(t, err))
  const rename = useMutation({
    mutationFn: () => jobsApi.rename(job.biz_id, name.trim()),
    onSuccess: () => {
      setPending(null)
      toast(t('done.renamed'))
      refresh()
    },
    onError,
  })
  const cancel = useMutation({
    mutationFn: () => jobsApi.cancel(job.biz_id),
    onSuccess: () => {
      setPending(null)
      toast(t('done.cancelling'))
      refresh()
    },
    onError,
  })
  const remove = useMutation({
    mutationFn: () => jobsApi.remove(job.biz_id),
    onSuccess: () => {
      setPending(null)
      toast(t('done.deleted'))
      refresh()
    },
    onError,
  })

  const items: MenuItem[] = [{ key: 'rename', label: t('action.rename'), onSelect: () => (setName(job.title), setPending('rename')) }]
  if ((ACTIVE_STATUSES as readonly string[]).includes(job.status)) {
    items.push({ key: 'cancel', label: t('action.cancel'), danger: true, onSelect: () => setPending('cancel') })
  }
  if ((TERMINAL_STATUSES as readonly string[]).includes(job.status)) {
    items.push({ key: 'delete', label: t('action.delete'), danger: true, onSelect: () => setPending('delete') })
  }
  const tooLong = [...name.trim()].length > 128

  return (
    <>
      <Menu trigger={<IconButton label={t('action.more')} icon={<Ellipsis className="size-5" />} />} items={items} />
      <Dialog
        open={pending === 'rename'}
        onOpenChange={(o) => !o && setPending(null)}
        title={t('rename.title')}
        locked={rename.isPending}
        footer={
          <>
            <Button onClick={() => setPending(null)} disabled={rename.isPending}>
              {t('ui:action.cancel')}
            </Button>
            <Button variant="primary" loading={rename.isPending} disabled={!name.trim() || tooLong} onClick={() => rename.mutate()}>
              {t('ui:action.save')}
            </Button>
          </>
        }
      >
        <form
          onSubmit={(e) => {
            e.preventDefault()
            if (name.trim() && !tooLong) rename.mutate()
          }}
        >
          <Field label={t('rename.label')} error={tooLong ? t('codes:api.invalid_title', { max: 128 }) : undefined}>
            <Input value={name} onChange={(e) => setName(e.target.value)} autoFocus maxLength={200} />
          </Field>
        </form>
      </Dialog>
      <ConfirmDialog
        open={pending === 'cancel'}
        onOpenChange={(o) => !o && setPending(null)}
        title={t('cancelConfirm.title')}
        target={title}
        effects={[t('cancelConfirm.effect1'), t('cancelConfirm.effect2')]}
        confirmLabel={t('cancelConfirm.confirm')}
        danger
        busy={cancel.isPending}
        onConfirm={() => cancel.mutate()}
      />
      <ConfirmDialog
        open={pending === 'delete'}
        onOpenChange={(o) => !o && setPending(null)}
        title={t('deleteConfirm.title')}
        target={title}
        effects={[t('deleteConfirm.effect1'), t('deleteConfirm.effect2')]}
        confirmLabel={t('deleteConfirm.confirm')}
        danger
        busy={remove.isPending}
        onConfirm={() => remove.mutate()}
      />
    </>
  )
}
