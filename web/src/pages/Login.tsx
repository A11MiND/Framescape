import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, ApiError } from '../lib/api'
import { useAuthStore } from '../lib/authStore'

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
      </form>
    </div>
  )
}
