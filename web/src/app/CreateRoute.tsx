import { lazy, useEffect } from 'react'
import { Navigate, useLocation, useParams } from 'react-router-dom'
import type { Tab } from '../lib/jobResult'
import { RequireAuth, isCreateMode, lastCreateMode, rememberCreateMode, type CreateMode } from './routing'

const Studio = lazy(() => import('../pages/Studio'))
const ComicStudio = lazy(() => import('../pages/ComicStudio'))

// Until each mode is rebuilt, the existing studio renders it; GPT image
// opens the image mode.
const STUDIO_TAB: Record<Exclude<CreateMode, 'comic'>, Tab> = {
  image: 'image.single',
  gpt: 'image.single',
  'image-sequence': 'image.sequence',
  video: 'video.single',
  'video-sequence': 'video.sequence',
  'comic-classic': 'image.comic4',
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
  if (mode === 'comic') {
    return (
      <RequireAuth>
        <ComicStudio />
      </RequireAuth>
    )
  }
  return <Studio key={mode} initialTab={STUDIO_TAB[mode]} />
}
