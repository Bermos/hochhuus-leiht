<script setup>
import { computed, onMounted, ref } from 'vue'
import { lang, setLang, t } from './i18n.js'
import { api } from './api.js'
import ItemCard from './components/ItemCard.vue'
import ItemEditor from './components/ItemEditor.vue'
import shareImage from './assets/share.webp'
import returnImage from './assets/return.webp'

const session = ref(null)
const items = ref([])
const loading = ref(true)
const loadFailed = ref(false)
const message = ref({ text: '', error: false })

const editing = ref(null) // null: closed, {}: new item, item: editing it
const editorKey = ref(0)
const removing = ref(null)
const removeDialog = ref(null)
const importInput = ref(null)

const query = ref('')
const category = ref('Alle')
const onlyMine = ref(false)

const code = ref('')
const loginError = ref('')
const signingIn = ref(false)

const ideas = ['Bohrmaschine', 'Heissluftgerät', 'Kärcher', 'Osterhasen-Giessformen']

function notify(text, error = false) {
  message.value = { text, error }
}

const filtered = computed(() => {
  const q = query.value.trim().toLocaleLowerCase('de-CH')
  return items.value.filter((i) =>
    (category.value === 'Alle' || i.category === category.value) &&
    (!onlyMine.value || i.mine) &&
    (!q || [i.title, i.description, i.name, i.floor, i.category, t(i.category)]
      .join(' ').toLocaleLowerCase('de-CH').includes(q)))
})
const hasMine = computed(() => items.value.some((i) => i.mine))

async function loadSession() {
  try {
    session.value = await api.session()
  } catch (e) {
    loadFailed.value = true
    loading.value = false
    return
  }
  if (session.value.signedIn) await loadItems()
  else loading.value = false
}

async function loadItems() {
  loading.value = true
  loadFailed.value = false
  try {
    items.value = await api.items()
  } catch (e) {
    if (e.status === 401) session.value = { ...session.value, signedIn: false }
    else loadFailed.value = true
  } finally {
    loading.value = false
  }
}

async function signIn() {
  signingIn.value = true
  loginError.value = ''
  try {
    const res = await api.login(code.value)
    session.value = { ...session.value, ...res }
    code.value = ''
    await loadItems()
  } catch (e) {
    loginError.value = e.message
  } finally {
    signingIn.value = false
  }
}

async function signOut() {
  await api.logout().catch(() => {})
  items.value = []
  editing.value = null
  message.value = { text: '', error: false }
  session.value = { ...session.value, signedIn: session.value.open, admin: false }
  if (session.value.open) await loadItems()
}

function startEdit(item) {
  editing.value = item || {}
  editorKey.value++
  message.value = { text: '', error: false }
}

function saved(item, isNew, photoError) {
  const i = items.value.findIndex((x) => x.id === item.id)
  if (i >= 0) items.value.splice(i, 1, item)
  else items.value.unshift(item)
  editing.value = null
  if (photoError) notify(photoError === 'Bilder sind nicht eingerichtet.' ? photoError : 'Der Eintrag wurde gespeichert, das Foto aber nicht.', true)
  else notify(isNew ? 'Dein Gegenstand steht jetzt in der Ausleihliste.' : 'Dein Eintrag wurde aktualisiert.')
}

function askRemove(item) {
  removing.value = item
  removeDialog.value?.showModal()
}

async function confirmRemove() {
  const item = removing.value
  removeDialog.value?.close()
  if (!item) return
  try {
    await api.remove(item.id)
    items.value = items.value.filter((x) => x.id !== item.id)
    if (editing.value?.id === item.id) editing.value = null
    notify('Der Eintrag wurde entfernt.')
  } catch (e) {
    notify(e.message || 'Löschen fehlgeschlagen.', true)
  }
}

// The backup has the offline edition's format, so either edition can open it.
function backup() {
  const fields = ['id', 'title', 'category', 'description', 'name', 'floor', 'contact', 'conditions', 'status']
  const data = {
    format: 'unser-haus-leiht',
    version: 1,
    exportedAt: new Date().toISOString(),
    items: items.value.map((i) => Object.fromEntries(fields.map((k) => [k, i[k] ?? '']))),
  }
  const url = URL.createObjectURL(new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' }))
  const a = document.createElement('a')
  a.href = url
  a.download = 'Ausleihliste_' + new Date().toISOString().replace(/[:.]/g, '-') + '.json'
  a.click()
  setTimeout(() => URL.revokeObjectURL(url), 2000)
  notify('Sicherung zum Download bereitgestellt. Sie enthält Kontaktangaben: bitte nur innerhalb der Hausgemeinschaft weitergeben.')
}

async function importBackup(event) {
  const file = event.target.files?.[0]
  event.target.value = ''
  if (!file) return
  try {
    if (file.size > 5 * 1024 * 1024) throw new Error('Die Datei ist zu gross (maximal 5 MB).')
    let data
    try { data = JSON.parse(await file.text()) } catch { throw new Error('Die Datei konnte nicht gelesen werden.') }
    const res = await api.importBackup(data)
    await loadItems()
    notify(`${res.added} ${t('Einträge hinzugefügt')}, ${res.skipped} ${t('bereits vorhanden')}.`)
  } catch (e) {
    notify(e.message, true)
  }
}

function print() {
  document.querySelectorAll('.card details').forEach((d) => { d.open = true })
  window.print()
}

onMounted(() => {
  setLang(lang.value)
  loadSession()
})
</script>

<template>
  <header>
    <span class="brand">{{ t('Hochhuus') }} <b>{{ t('leiht.') }}</b></span>
    <span class="header-note">{{ t(session?.open ? 'Online-Ausgabe' : 'Geschützte Hausgemeinschaft') }}</span>
    <div class="header-actions">
      <div class="language" role="group" aria-label="Sprache / Language">
        <button type="button" :aria-pressed="String(lang === 'de')" @click="setLang('de')">DE (CH)</button>
        <button type="button" :aria-pressed="String(lang === 'en')" @click="setLang('en')">EN (UK)</button>
      </div>
      <button v-if="session?.signedIn && !session.open" type="button" class="header-button" @click="signOut">{{ t('Abmelden') }}</button>
    </div>
  </header>

  <main>
    <section class="intro">
      <div>
        <p class="small">{{ t('DIE AUSLEIHLISTE IM HAUS') }}</p>
        <h1>{{ t('Vielleicht hat’s') }}<br>{{ t('jemand nebenan.') }}</h1>
        <p>{{ t('Finden, anfragen, ausleihen. Was du selten brauchst,') }} {{ t('muss nicht in jedem Haushalt stehen.') }}</p>
      </div>
      <img :src="shareImage" width="1200" height="800" :alt="t('Illustration: Nachbarn teilen Werkzeuge und Giessformen vor dem Hochhuus')">
    </section>

    <p v-if="!session && loading" class="small">{{ t('Die Ausleihliste wird geladen …') }}</p>

    <div v-else-if="loadFailed && !session" class="empty">
      <p>{{ t('Die Liste konnte nicht geladen werden. Bitte nochmals versuchen.') }}</p>
      <button type="button" @click="loadSession">{{ t('Erneut laden') }}</button>
    </div>

    <section v-else-if="!session.signedIn" class="editor login">
      <h2>{{ t('Bitte den Hauscode eingeben.') }}</h2>
      <p class="small">{{ t('Nur freigeschaltete Personen haben Online-Zugang.') }} {{ t('Den Code erhältst du von deinen Nachbarinnen und Nachbarn.') }}</p>
      <form class="login-form" @submit.prevent="signIn">
        <label>{{ t('Hauscode') }}
          <input v-model="code" type="password" autocomplete="current-password" required>
        </label>
        <button class="primary" type="submit" :disabled="signingIn">{{ t('Anmelden') }}</button>
      </form>
      <p v-if="loginError" class="notice error" role="alert">{{ t(loginError) }}</p>
    </section>

    <template v-else>
      <div class="toolbar">
        <button class="primary" type="button" @click="startEdit()">+ {{ t('Gegenstand anbieten') }}</button>
        <button type="button" @click="loadItems">{{ t('Liste aktualisieren') }}</button>
        <button type="button" @click="print">{{ t('Drucken / PDF') }}</button>
        <template v-if="session.admin">
          <button type="button" @click="importInput.click()">{{ t('Sicherung einlesen') }}</button>
          <input ref="importInput" type="file" accept="application/json,.json" hidden @change="importBackup">
        </template>
      </div>

      <p v-if="message.text" class="notice" :class="{ error: message.error }" role="status">{{ t(message.text) }}</p>

      <ItemEditor
        v-if="editing"
        :key="editorKey"
        :item="editing.id ? editing : null"
        :categories="session.categories"
        :statuses="session.statuses"
        :pictures="session.pictures"
        @saved="saved"
        @cancel="editing = null"
      />

      <div class="searchbar">
        <input v-model="query" type="search" :aria-label="t('Gegenstände suchen')" :placeholder="t('Bohrmaschine, Giessform, Kärcher …')">
        <select v-model="category" :aria-label="t('Nach Kategorie filtern')">
          <option value="Alle">{{ t('Alle Kategorien') }}</option>
          <option v-for="c in session.categories" :key="c" :value="c">{{ t(c) }}</option>
        </select>
        <label v-if="hasMine" class="checkbox">
          <input v-model="onlyMine" type="checkbox"> {{ t('Meine Einträge') }}
        </label>
      </div>

      <h2>{{ t('Was gibt’s im Haus?') }} · {{ items.length }}</h2>

      <p v-if="loading" class="small">{{ t('Die Ausleihliste wird geladen …') }}</p>
      <div v-else-if="loadFailed" class="empty">
        <p>{{ t('Die Liste konnte nicht geladen werden. Bitte nochmals versuchen.') }}</p>
        <button type="button" @click="loadItems">{{ t('Erneut laden') }}</button>
      </div>
      <div v-else-if="items.length === 0" class="empty">
        <h3>{{ t('Die ersten Dinge machen den Anfang.') }}</h3>
        <p>{{ t('Trage ein, was du gerne ausleihst. Hier sind ein paar Ideen – noch keine tatsächlichen Angebote.') }}</p>
        <p class="ideas"><span v-for="idea in ideas" :key="idea" class="tag">{{ t(idea) }}</span></p>
      </div>
      <div v-else-if="filtered.length === 0" class="empty">
        <h3>{{ t('Nichts Passendes gefunden.') }}</h3>
        <p>{{ t('Probiere einen anderen Suchbegriff oder eine andere Kategorie.') }}</p>
      </div>
      <div v-else class="cards">
        <ItemCard v-for="item in filtered" :key="item.id" :item="item" @edit="startEdit" @remove="askRemove" />
      </div>

      <section class="instructions offline">
        <h2>{{ t('Auch ohne Internet dabei.') }}</h2>
        <p>{{ t('Lade die Offline-Anwendung und eine Kopie der aktuellen Liste herunter. Änderungen bleiben auf deinem Gerät; sie werden nicht automatisch mit der Hausliste abgeglichen.') }}</p>
        <div class="toolbar">
          <a class="button" href="/offline/Hochhuus-Offline.html" download="Hochhuus-Offline.html">{{ t('Offline-Anwendung herunterladen') }}</a>
          <button type="button" @click="backup">{{ t('Aktuelle Liste sichern') }}</button>
        </div>
        <p class="small">{{ t('Auf dem Mac die HTML-Datei öffnen, dann «Liste aus Datei öffnen» wählen und die JSON-Sicherung einlesen. Ohne Anmeldung nutzbar. Sicherungen enthalten die Kontaktangaben der Hausgemeinschaft.') }}</p>
      </section>
    </template>

    <div class="footer">
      <img :src="returnImage" width="1200" height="800" loading="lazy" :alt="t('Illustration: Nachbarn geben geliehene Haushaltsgeräte zurück')">
      <p>
        <strong>{{ t('So einfach geht’s.') }}</strong><br>
        {{ t('Gegenstand finden · direkt Kontakt aufnehmen · Rückgabe vereinbaren.') }}<br>
        {{ t('Jede Person pflegt ihre eigenen Einträge.') }} {{ t('Nur freigeschaltete Personen haben Online-Zugang.') }}<br>
        <span class="small">{{ t('Illustrationen KI-generiert.') }}</span>
      </p>
    </div>
  </main>

  <dialog ref="removeDialog" class="dialog" @close="removing = null">
    <form method="dialog">
      <h2>{{ t('Eintrag entfernen?') }}</h2>
      <p>{{ t('«') }}{{ removing?.title }}{{ t('» wird aus der gemeinsamen Liste gelöscht.') }}</p>
      <div class="actions">
        <button type="button" class="primary" @click="confirmRemove">{{ t('Entfernen') }}</button>
        <button value="cancel">{{ t('Behalten') }}</button>
      </div>
    </form>
  </dialog>
</template>
