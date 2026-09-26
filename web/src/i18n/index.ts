import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import zh from './zh.json'
import en from './en.json'
import zhUi from './locales/zh/ui.json'
import enUi from './locales/en/ui.json'
import zhCodes from './locales/zh/codes.json'
import enCodes from './locales/en/codes.json'
import zhShell from './locales/zh/shell.json'
import enShell from './locales/en/shell.json'
import zhTasks from './locales/zh/tasks.json'
import enTasks from './locales/en/tasks.json'
import zhJob from './locales/zh/job.json'
import enJob from './locales/en/job.json'
import zhCreate from './locales/zh/create.json'
import enCreate from './locales/en/create.json'
import zhComic from './locales/zh/comic.json'
import enComic from './locales/en/comic.json'
import zhLibrary from './locales/zh/library.json'
import enLibrary from './locales/en/library.json'
import zhProjects from './locales/zh/projects.json'
import enProjects from './locales/en/projects.json'
import zhCharacters from './locales/zh/characters.json'
import enCharacters from './locales/en/characters.json'
import zhPresets from './locales/zh/presets.json'
import enPresets from './locales/en/presets.json'

// The flat "translation" namespace belongs to pages not yet rebuilt; new
// code uses one namespace per area (ui, codes, shell, and one per feature).
const STORAGE_KEY = 'aigc.lang'

export type Lang = 'zh' | 'en'

export function getStoredLang(): Lang {
  try {
    return localStorage.getItem(STORAGE_KEY) === 'en' ? 'en' : 'zh'
  } catch {
    return 'zh'
  }
}

export function setStoredLang(lang: Lang) {
  try {
    localStorage.setItem(STORAGE_KEY, lang)
  } catch {
    // the choice still applies for this visit
  }
  i18n.changeLanguage(lang)
}

export const resources = {
  zh: { translation: zh, ui: zhUi, codes: zhCodes, shell: zhShell, tasks: zhTasks, job: zhJob, create: zhCreate, comic: zhComic, library: zhLibrary, projects: zhProjects, characters: zhCharacters, presets: zhPresets },
  en: { translation: en, ui: enUi, codes: enCodes, shell: enShell, tasks: enTasks, job: enJob, create: enCreate, comic: enComic, library: enLibrary, projects: enProjects, characters: enCharacters, presets: enPresets },
} as const

i18n.use(initReactI18next).init({
  resources,
  lng: getStoredLang(),
  fallbackLng: 'zh',
  defaultNS: 'translation',
  interpolation: { escapeValue: false },
})

i18n.on('languageChanged', (lng) => {
  document.documentElement.lang = lng === 'en' ? 'en' : 'zh-CN'
})
document.documentElement.lang = i18n.language === 'en' ? 'en' : 'zh-CN'

export default i18n
