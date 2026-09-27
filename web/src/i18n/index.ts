import enauthV2 from './locales/en/authV2.json'
import zhauthV2 from './locales/zh/authV2.json'
import enadminV2 from './locales/en/adminV2.json'
import zhadminV2 from './locales/zh/adminV2.json'
import i18n from 'i18next'
import { initReactI18next } from 'react-i18next'
import zh from './zh.json'
import en from './en.json'
import zhTWBase from './locales/zh-TW-base.json'
import zhAuthV2TW from './locales/zh-TW/authV2.json'
import zhAdminV2TW from './locales/zh-TW/adminV2.json'
import zhUiTW from './locales/zh-TW/ui.json'
import zhCodesTW from './locales/zh-TW/codes.json'
import zhShellTW from './locales/zh-TW/shell.json'
import zhTasksTW from './locales/zh-TW/tasks.json'
import zhJobTW from './locales/zh-TW/job.json'
import zhCreateTW from './locales/zh-TW/create.json'
import zhComicTW from './locales/zh-TW/comic.json'
import zhLibraryTW from './locales/zh-TW/library.json'
import zhProjectsTW from './locales/zh-TW/projects.json'
import zhCharactersTW from './locales/zh-TW/characters.json'
import zhPresetsTW from './locales/zh-TW/presets.json'
import zhCommunityTW from './locales/zh-TW/community.json'
import zhCreditsTW from './locales/zh-TW/credits.json'
import zhSettingsTW from './locales/zh-TW/settings.json'
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
import zhCommunity from './locales/zh/community.json'
import enCommunity from './locales/en/community.json'
import zhCredits from './locales/zh/credits.json'
import enCredits from './locales/en/credits.json'
import zhSettings from './locales/zh/settings.json'
import enSettings from './locales/en/settings.json'

// The flat "translation" namespace belongs to pages not yet rebuilt; new
// code uses one namespace per area (ui, codes, shell, and one per feature).
const STORAGE_KEY = 'aigc.lang'

export type Lang = 'zh' | 'zh-TW' | 'en'

export function getStoredLang(): Lang {
  try {
    const value = localStorage.getItem(STORAGE_KEY)
    return value === 'en' || value === 'zh-TW' ? value : 'zh'
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
  zh: { authV2: zhauthV2, adminV2: zhadminV2, translation: zh, ui: zhUi, codes: zhCodes, shell: zhShell, tasks: zhTasks, job: zhJob, create: zhCreate, comic: zhComic, library: zhLibrary, projects: zhProjects, characters: zhCharacters, presets: zhPresets, community: zhCommunity, credits: zhCredits, settings: zhSettings },
  'zh-TW': { authV2: zhAuthV2TW, adminV2: zhAdminV2TW, translation: zhTWBase, ui: zhUiTW, codes: zhCodesTW, shell: zhShellTW, tasks: zhTasksTW, job: zhJobTW, create: zhCreateTW, comic: zhComicTW, library: zhLibraryTW, projects: zhProjectsTW, characters: zhCharactersTW, presets: zhPresetsTW, community: zhCommunityTW, credits: zhCreditsTW, settings: zhSettingsTW },
  en: { authV2: enauthV2, adminV2: enadminV2, translation: en, ui: enUi, codes: enCodes, shell: enShell, tasks: enTasks, job: enJob, create: enCreate, comic: enComic, library: enLibrary, projects: enProjects, characters: enCharacters, presets: enPresets, community: enCommunity, credits: enCredits, settings: enSettings },
} as const

i18n.use(initReactI18next).init({
  resources,
  lng: getStoredLang(),
  supportedLngs: ['zh', 'zh-TW', 'en'],
  fallbackLng: 'zh',
  defaultNS: 'translation',
  interpolation: { escapeValue: false },
})

i18n.on('languageChanged', (lng) => {
  document.documentElement.lang = lng === 'en' ? 'en' : lng === 'zh-TW' ? 'zh-TW' : 'zh-CN'
})
document.documentElement.lang = i18n.language === 'en' ? 'en' : i18n.language === 'zh-TW' ? 'zh-TW' : 'zh-CN'

export default i18n
