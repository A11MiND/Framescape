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
    <div className="flex min-h-screen items-center justify-center bg-zinc-950">
      <form onSubmit={submit} className="w-80 rounded-xl border border-zinc-800 bg-zinc-900 p-6">
        <h1 className="mb-4 text-lg font-medium text-zinc-50">
          {mode === 'login' ? '登录' : '注册'} AIGC 平台
        </h1>
        <input
          type="email"
          required
          placeholder="邮箱"
          value={email}
          onChange={(e) => setEmail(e.target.value)}
          className="mb-3 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-zinc-50 outline-none focus:border-violet-500"
        />
        <input
          type="password"
          required
          minLength={8}
          placeholder="密码（至少 8 位）"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          className="mb-3 w-full rounded-lg border border-zinc-800 bg-zinc-950 px-3 py-2 text-zinc-50 outline-none focus:border-violet-500"
        />
        {error && <p className="mb-3 text-sm text-red-400">{error}</p>}
        <button
          type="submit"
          disabled={busy}
          className="w-full rounded-lg bg-violet-500 px-3 py-2 font-medium text-white transition hover:bg-violet-400 disabled:opacity-50"
        >
          {busy ? '处理中…' : mode === 'login' ? '登录' : '注册'}
        </button>
        <button
          type="button"
          onClick={() => setMode(mode === 'login' ? 'register' : 'login')}
          className="mt-3 w-full text-sm text-zinc-400 hover:text-zinc-200"
        >
          {mode === 'login' ? '没有账号？去注册' : '已有账号？去登录'}
        </button>

        <TrialSection />
      </form>
    </div>
  )
}

// F1.2: one anonymous single-image generation before requiring signup
// ("先给价值再要注册"). Self-contained — doesn't touch jobs/credits/assets,
// see trial.go's doc for why.
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
    <div className="mt-6 border-t border-zinc-800 pt-4">
      <p className="mb-2 text-xs uppercase tracking-wide text-zinc-500">
        或者不注册，先匿名试用一次单图生成（F1.2）
      </p>
      <textarea
        value={prompt}
        onChange={(e) => setPrompt(e.target.value)}
        rows={2}
        className="mb-2 w-full resize-none rounded-lg border border-zinc-800 bg-zinc-950 p-2 text-sm text-zinc-50 outline-none focus:border-violet-500"
      />
      <button
        type="button"
        onClick={tryOnce}
        disabled={busy || !prompt.trim()}
        className="w-full rounded-lg border border-zinc-700 px-3 py-2 text-sm text-zinc-200 transition hover:bg-zinc-800 disabled:opacity-50"
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
