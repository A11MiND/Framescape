import { lazy, useEffect } from 'react'
import { Navigate, useLocation, useParams } from 'react-router-dom'
import type { Tab } from '../lib/jobResult'
import { CreateNav } from '../features/create/CreateNav'
import { RequireAuth } from './guards'
import { isCreateMode, lastCreateMode, rememberCreateMode, type CreateMode } from './routing'

const Studio = lazy(() => import('../pages/Studio'))
const ComicStudio = lazy(() => import('../pages/ComicStudio'))
const ImageStudio = lazy(() => import('../features/create/ImageStudio'))
const GptStudio = lazy(() => import('../features/create/GptStudio'))
const SequenceStudio = lazy(() => import('../features/create/SequenceStudio'))
const VideoStudio = lazy(() => import('../features/create/VideoStudio'))
const VideoSequenceStudio = lazy(() => import('../features/create/VideoSequenceStudio'))

// Modes not rebuilt yet keep the existing studio form under the new mode navigation.
const LEGACY_TAB: Partial<Record<CreateMode, Tab>> = {
  'comic-classic': 'image.comic4',
}

function Legacy({ mode, children }: { mode: CreateMode; children: React.ReactNode }) {
  return (
    <>
      <div className="mx-auto max-w-[1440px] px-4 pt-6 lg:px-6">
        <CreateNav mode={mode} />
      </div>
      {children}
    </>
  )
}

export default function CreateRoute() {
  const { mode } = useParams()
  const location = useLocation()
  const valid = isCreateMode(mode)
  useEffect(() => {
    if (valid) rememberCreateMode(mode)
  }, [valid, mode])
  if (!valid) {
    return <Navigate replace to={{ pathname: `/create/${lastCreateMode()}`, search: location.search }} state={location.state} />
  }
  if (mode === 'image') return <ImageStudio />
  if (mode === 'video-sequence') {
    return (
      <RequireAuth>
        <VideoSequenceStudio />
      </RequireAuth>
    )
  }
  if (mode === 'video') {
    return (
      <RequireAuth>
        <VideoStudio />
      </RequireAuth>
    )
  }
  if (mode === 'image-sequence') {
    return (
      <RequireAuth>
        <SequenceStudio />
      </RequireAuth>
    )
  }
  if (mode === 'gpt') {
    return (
      <RequireAuth>
        <GptStudio />
      </RequireAuth>
    )
  }
  if (mode === 'comic') {
    return (
      <Legacy mode={mode}>
        <RequireAuth>
          <ComicStudio />
        </RequireAuth>
      </Legacy>
    )
  }
  return (
    <Legacy mode={mode}>
      <Studio key={mode} initialTab={LEGACY_TAB[mode]} embedded />
    </Legacy>
  )
}
