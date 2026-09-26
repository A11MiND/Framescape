import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button, ConfirmDialog, Dialog, Field, Input, Textarea } from '../../ui'
import { projectsApi, type ProjectSummary } from '../../lib/api/projects'
import { keys } from '../../lib/api/keys'
import { errorText } from '../../lib/errorText'
import { useToast } from '../../components/Toast'

/** Creates a project, or edits one when `project` is given. */
export function ProjectForm({ open, onOpenChange, project, onSaved }: { open: boolean; onOpenChange: (o: boolean) => void; project?: ProjectSummary; onSaved?: (p: ProjectSummary) => void }) {
  const { t } = useTranslation('projects')
  const qc = useQueryClient()
  const toast = useToast()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  useEffect(() => {
    if (!open) return
    setName(project?.name ?? '')
    setDescription(project?.description ?? '')
  }, [open, project])
  const save = useMutation({
    mutationFn: () => (project ? projectsApi.update(project.biz_id, name.trim(), description) : projectsApi.create(name.trim(), description)),
    onSuccess: (p) => {
      toast(project ? t('saved') : t('createdDone'))
      qc.invalidateQueries({ queryKey: keys.projects.all })
      onOpenChange(false)
      onSaved?.(p)
    },
    onError: (err) => toast(errorText(t, err)),
  })
  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={project ? t('form.editTitle') : t('form.createTitle')}
      size="form"
      locked={save.isPending}
      footer={
        <>
          <Button onClick={() => onOpenChange(false)} disabled={save.isPending}>
            {t('ui:action.cancel')}
          </Button>
          <Button variant="primary" loading={save.isPending} disabled={!name.trim()} onClick={() => save.mutate()}>
            {project ? t('form.save') : t('form.createConfirm')}
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-4">
        <Field label={t('form.name')}>
          <Input value={name} maxLength={128} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label={t('form.description')}>
          <Textarea value={description} maxChars={500} onChange={(e) => setDescription(e.target.value)} />
        </Field>
      </div>
    </Dialog>
  )
}

/** Deleting a project states what it holds and that all of it is kept, unassigned. */
export function DeleteProject({ project, onOpenChange, onDeleted }: { project: ProjectSummary | null; onOpenChange: (o: boolean) => void; onDeleted?: () => void }) {
  const { t } = useTranslation('projects')
  const qc = useQueryClient()
  const toast = useToast()
  const remove = useMutation({
    mutationFn: (id: string) => projectsApi.remove(id),
    onSuccess: (res) => {
      const d = res.detached
      toast(t('deleteDialog.done', { assets: d.assets ?? 0, jobs: d.jobs ?? 0, characters: d.characters ?? 0 }))
      qc.invalidateQueries({ queryKey: keys.projects.all })
      qc.invalidateQueries({ queryKey: keys.assets.all })
      qc.invalidateQueries({ queryKey: keys.jobs.all })
      onOpenChange(false)
      onDeleted?.()
    },
    onError: (err) => toast(errorText(t, err)),
  })
  return (
    <ConfirmDialog
      open={project !== null}
      onOpenChange={onOpenChange}
      title={project ? t('deleteDialog.title', { name: project.name }) : ''}
      body={t('deleteDialog.body')}
      effects={
        project
          ? [t('deleteDialog.assets', { n: project.asset_count }), t('deleteDialog.jobs', { n: project.job_count }), t('deleteDialog.characters', { n: project.character_count })]
          : []
      }
      confirmLabel={t('deleteDialog.confirm')}
      danger
      busy={remove.isPending}
      onConfirm={() => project && remove.mutate(project.biz_id)}
    />
  )
}
