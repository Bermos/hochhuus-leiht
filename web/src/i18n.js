import { ref } from 'vue'
import translations from './translations.json'

// German is the source language; English is looked up by the German text,
// exactly as the offline edition does it.
function initial() {
  try {
    const saved = localStorage.getItem('house-language')
    if (saved === 'en' || saved === 'de') return saved
  } catch {}
  return navigator.language?.startsWith('de') ? 'de' : (navigator.language ? 'en' : 'de')
}

export const lang = ref(initial())

export function setLang(value) {
  lang.value = value
  document.documentElement.lang = value === 'en' ? 'en-GB' : 'de-CH'
  document.title = value === 'en' ? 'Hochhuus shares.' : 'Hochhuus leiht.'
  try { localStorage.setItem('house-language', value) } catch {}
}

export function t(text) {
  return lang.value === 'en' ? (translations[text] ?? text) : text
}
