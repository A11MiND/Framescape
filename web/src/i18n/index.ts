import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import zh from './zh.json'
import en from './en.json'

// Language preference is a plain localStorage read/write, not
// i18next-browser-languagedetector — the whole app already hand-rolls its
// own persistence for auth/theme rather than pulling in a detector plugin
// for what's just one string, so this stays consistent with that.
const STORAGE_KEY = 'aigc.lang'

export type Lang = 'zh' | 'en'

export function getStoredLang(): Lang {
  const v = localStorage.getItem(STORAGE_KEY)
  return v === 'en' ? 'en' : 'zh'
}

export function setStoredLang(lang: Lang) {
  localStorage.setItem(STORAGE_KEY, lang)
  i18n.changeLanguage(lang)
}

i18n.use(initReactI18next).init({
  resources: {
    zh: { translation: zh },
    en: { translation: en },
  },
  lng: getStoredLang(),
  fallbackLng: 'zh',
  interpolation: { escapeValue: false },
})

export default i18n
