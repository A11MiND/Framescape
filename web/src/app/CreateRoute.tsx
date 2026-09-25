import { lazy, useEffect } from 'react'
import { Navigate, useLocation, useParams } from 'react-router-dom'
import { CreateNav } from '../features/create/CreateNav'
import { RequireAuth } from './guards'
import { isCreateMode, lastCreateMode, rememberCreateMode, type CreateMode } from './routing'

const ComicStudio = lazy(() => import('../pages/ComicStudio'))
const ImageStudio = lazy(() => import('../features/create/ImageStudio'))
const GptStudio = lazy(() => import('../features/create/GptStudio'))
const SequenceStudio = lazy(() => import('../features/create/SequenceStudio'))
const VideoStudio = lazy(() => import('../features/create/VideoStudio'))
const VideoSequenceStudio = lazy(() => import('../features/create/VideoSequenceStudio'))
const ComicClassicStudio = lazy(() => import('../features/create/ComicClassicStudio'))

// The comic editor keeps its own page layout under the mode navigation.
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
  if (mode === 'comic-classic') {
    return (
      <RequireAuth>
        <ComicClassicStudio />
      </RequireAuth>
    )
  }
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
  return (
    <Legacy mode={mode}>
      <RequireAuth>
        <ComicStudio />
      </RequireAuth>
    </Legacy>
  )
}
