import { api, type AssetResponse } from './api'

// Reads intrinsic width/height (and duration for video) from a File
// entirely client-side — the one piece of metadata the server genuinely
// can't get without downloading and decoding the upload itself (F2.1's
// handleCompleteAsset trusts the object store's own Stat for mime/size, but
// has no equivalent source for these, see that handler's doc).
function readMediaMeta(file: File): Promise<{ width: number; height: number; durationMs: number }> {
  return new Promise((resolve) => {
    const url = URL.createObjectURL(file)
    const done = (meta: { width: number; height: number; durationMs: number }) => {
      URL.revokeObjectURL(url)
      resolve(meta)
    }
    if (file.type.startsWith('image/')) {
      const img = new Image()
      img.onload = () => done({ width: img.naturalWidth, height: img.naturalHeight, durationMs: 0 })
      img.onerror = () => done({ width: 0, height: 0, durationMs: 0 })
      img.src = url
    } else if (file.type.startsWith('video/')) {
      const video = document.createElement('video')
      video.preload = 'metadata'
      video.onloadedmetadata = () =>
        done({ width: video.videoWidth, height: video.videoHeight, durationMs: Math.round(video.duration * 1000) })
      video.onerror = () => done({ width: 0, height: 0, durationMs: 0 })
      video.src = url
    } else {
      done({ width: 0, height: 0, durationMs: 0 })
    }
  })
}

// F2.1's full direct-upload flow: presign, PUT straight to object storage
// (never through cmd/api — PRD §11's "不经过 Go 服务"), then confirm. The
// one client-supplied upload affordance in the app; AssetPicker calls this
// so every picker that embeds it gets upload for free.
export async function uploadAsset(file: File): Promise<AssetResponse> {
  const { biz_id, upload_url, storage_key } = await api.getUploadURL(file.name, file.type || 'application/octet-stream')
  await api.uploadToPresignedURL(upload_url, file)
  const meta = await readMediaMeta(file)
  return api.completeAsset(biz_id, {
    storage_key,
    width: meta.width,
    height: meta.height,
    duration_ms: meta.durationMs,
  })
}
