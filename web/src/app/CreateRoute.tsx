import { lazy, useEffect } from 'react'
import { Navigate, useLocation, useParams } from 'react-router-dom'
import { RequireAuth } from './guards'
import { isCreateMode, lastCreateMode, rememberCreateMode } from './routing'

const ComicWorkspace = lazy(() => import('../features/comic/ComicWorkspace'))
const ImageStudio = lazy(() => import('../features/create/ImageStudio'))
const GptStudio = lazy(() => import('../features/create/GptStudio'))
const SequenceStudio = lazy(() => import('../features/create/SequenceStudio'))
const VideoStudio = lazy(() => import('../features/create/VideoStudio'))
const VideoSequenceStudio = lazy(() => import('../features/create/VideoSequenceStudio'))
const ComicClassicStudio = lazy(() => import('../features/create/ComicClassicStudio'))

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
    <RequireAuth>
      <ComicWorkspace />
    </RequireAuth>
  )
}
