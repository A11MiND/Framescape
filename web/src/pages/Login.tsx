import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, ApiError } from '../lib/api'
import { useAuthStore } from '../lib/authStore'
import { getDeviceId } from '../lib/deviceId'

// F1.1: email register/login. Minimal single-form W1 version — no
// separate register/login screens yet, just a mode toggle.
export default function Login() {
  const [mode, setMode] = useState<'login' | 'register'>('login')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const setTokens = useAuthStore((s) => s.setTokens)
  const navigate = useNavigate()

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setError(null)
    setBusy(true)
    try {
      const tokens = mode === 'login' ? await api.login(email, password) : await api.register(email, password)
      setTokens(tokens.access_token, tokens.refresh_token)
      navigate('/studio')
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Something went wrong')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="relative flex min-h-screen items-center justify-center overflow-hidden bg-zinc-950 p-6">
      {/* Ambient glow — the app has no logo asset, so a soft brand-colored
          wash behind the card is the lightest-touch way to keep this page
          from reading as a bare, un-designed void. */}
      <div className="pointer-events-none absolute inset-0 overflow-hidden">
        <div className="absolute left-1/2 top-[28%] h-[520px] w-[520px] -translate-x-1/2 -translate-y-1/2 rounded-full bg-violet-600/20 blur-[140px]" />
      </div>

      <div className="relative w-full max-w-sm space-y-5">
        <div className="text-center">
          <div className="mx-auto mb-3 flex h-12 w-12 items-center justify-center rounded-2xl bg-gradient-to-br from-violet-400 to-violet-600 text-xl text-white shadow-lg shadow-violet-950/40">
            ✦
          </div>
          <h1 className="text-xl font-semibold text-zinc-50">AIGC 创作平台</h1>
          <p className="mt-1 text-sm text-zinc-500">一句话，生成图片与影片</p>
        </div>

        <form onSubmit={submit} className="rounded-2xl border border-zinc-800 bg-zinc-900/90 p-6 shadow-2xl shadow-black/40">
          <div className="mb-5 flex rounded-lg bg-zinc-950 p-1">
            {(['login', 'register'] as const).map((m) => (
              <button
                key={m}
                type="button"
                onClick={() => setMode(m)}
                className={`flex-1 rounded-md py-1.5 text-sm font-medium transition ${
                  mode === m ? 'bg-violet-500 text-white shadow' : 'text-zinc-500 hover:text-zinc-300'
                }`}
              >
                {m === 'login' ? '登录' : '注册'}
              </button>
            ))}
          </div>

          <div className="space-y-3">
            <input
              type="email"
              required
              placeholder="邮箱"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2.5 text-zinc-50 outline-none transition focus:border-violet-500"
            />
            <input
              type="password"
              required
              minLength={8}
              placeholder="密码（至少 8 位）"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2.5 text-zinc-50 outline-none transition focus:border-violet-500"
            />
          </div>

          {error && <p className="mt-3 text-sm text-red-400">{error}</p>}

          <button
            type="submit"
            disabled={busy}
            className="mt-4 w-full rounded-lg bg-violet-500 px-3 py-2.5 font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
          >
            {busy ? '处理中…' : mode === 'login' ? '登录' : '创建账号'}
          </button>
        </form>

        <div className="flex items-center gap-3 text-xs text-zinc-600">
          <div className="h-px flex-1 bg-zinc-800" />
          还没想好？
          <div className="h-px flex-1 bg-zinc-800" />
        </div>

        <TrialSection />
      </div>
    </div>
  )
}

// F1.2: one anonymous single-image generation before requiring signup
// ("先给价值再要注册"). Self-contained — doesn't touch jobs/credits/assets,
// see trial.go's doc for why. Its own card, not nested in the login <form>:
// it's a genuinely separate action (different submit handler, different
// backend endpoint), not a footnote on registration.
function TrialSection() {
  const [prompt, setPrompt] = useState('两人在天台对峙，黄昏逆光，风很大')
  const [imageUrl, setImageUrl] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function tryOnce() {
    setBusy(true)
    setError(null)
    try {
      const res = await api.trialImage(prompt, getDeviceId())
      setImageUrl(res.image_url)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : '生成失败，请重试')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rounded-2xl border border-dashed border-zinc-800 bg-zinc-900/40 p-5">
      <p className="mb-3 text-sm text-zinc-400">不用注册，先免费试用一次单图生成</p>
      <textarea
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
        rows={2}
        className="mb-2 w-full resize-none rounded-lg border border-zinc-800 bg-zinc-950 p-2 text-sm text-zinc-50 outline-none transition focus:border-violet-500"
      />
      <button
        type="button"
        onClick={tryOnce}
        disabled={busy || !prompt.trim()}
        className="w-full rounded-lg border border-zinc-700 px-3 py-2 text-sm text-zinc-200 transition hover:border-zinc-600 hover:bg-zinc-800 disabled:opacity-50"
      >
        {busy ? '生成中…' : '✦ 匿名试用一次'}
      </button>
      {error && <p className="mt-2 text-sm text-red-400">{error}</p>}
      {imageUrl && (
        <div className="mt-3 text-center">
          <img src={imageUrl} alt="" className="mx-auto rounded-lg" />
          <p className="mt-2 text-xs text-zinc-500">喜欢这张？注册账号才能保存到素材库</p>
        </div>
      )}
    </div>
  )
}
