import { z } from 'zod'

// Mirrors minimax.video's buildContent (internal/infra/executor/minimax/video.go):
// ratio must be one of these six for t2va; "adaptive" is only valid for
// i2va/r2va, never as a user choice for pure text-to-video.
export const RATIO_VALUES = ['21:9', '16:9', '4:3', '1:1', '3:4', '9:16'] as const

// PRD §3.2/§19.4.1's F6.5, "双保险": this schema is the first guard (checked
// right before submit); Studio's video.single tab greys out the conflicting
// picker as the second, so violating this should be structurally impossible
// via the UI — this exists to catch it anyway if it ever isn't.
export const videoSingleSchema = z
  .object({
    text: z.string().trim().min(1, '请输入视频描述'),
    duration: z.number().int().min(4).max(15),
    resolution: z.enum(['768P', '2K']),
    ratio: z.string(),
    firstFrameAssetId: z.string(),
    lastFrameAssetId: z.string(),
    referenceImageAssetIds: z.array(z.string()),
    referenceVideoAssetIds: z.array(z.string()),
  })
  .superRefine((v, ctx) => {
    const hasFirstLast = v.firstFrameAssetId !== '' || v.lastFrameAssetId !== ''
    const hasRef = v.referenceImageAssetIds.length > 0 || v.referenceVideoAssetIds.length > 0
    if (hasFirstLast && hasRef) {
      ctx.addIssue({
        code: 'custom',
        message: 'F6.5：首尾帧模式和参考素材模式不能同时使用',
      })
    }
    if (!hasFirstLast && !hasRef && !RATIO_VALUES.includes(v.ratio as (typeof RATIO_VALUES)[number])) {
      ctx.addIssue({ code: 'custom', message: '纯文字生成视频必须选择画面比例', path: ['ratio'] })
    }
  })

export type VideoSingleInput = z.infer<typeof videoSingleSchema>
