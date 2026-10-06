<script setup>
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { t } from '../i18n.js'
import { api, shrink } from '../api.js'

const props = defineProps({
  item: { type: Object, default: null },
  categories: { type: Array, required: true },
  statuses: { type: Array, required: true },
  pictures: { type: Boolean, default: false },
})
const emit = defineEmits(['saved', 'cancel'])

const blank = { title: '', category: props.categories[0], description: '', name: '', floor: '', contact: '', conditions: '', status: props.statuses[0] }
const form = reactive({ ...blank, ...(props.item ? pick(props.item) : rememberedContact()) })
const saving = ref(false)
const error = ref('')
const titleInput = ref(null)
const root = ref(null)

// The photo: a newly chosen file, or the removal of the existing one.
const photo = ref(null)
const photoPreview = ref('')
const removePhoto = ref(false)
const shownPhoto = computed(() => photoPreview.value || (!removePhoto.value && props.item?.imageUrl) || '')

function pick(item) {
  const out = {}
  for (const k of Object.keys(blank)) out[k] = item[k] ?? ''
  return out
}

// A resident lists several things under the same name, so offer it again.
function rememberedContact() {
  try { return JSON.parse(localStorage.getItem('house-contact')) || {} } catch { return {} }
}
function rememberContact() {
  try { localStorage.setItem('house-contact', JSON.stringify({ name: form.name, floor: form.floor, contact: form.contact })) } catch {}
}

function choose(event) {
  const file = event.target.files?.[0]
  if (!file) return
  if (file.size > 15 * 1024 * 1024) {
    error.value = 'Das Bild ist zu gross (maximal 15 MB).'
    event.target.value = ''
    return
  }
  error.value = ''
  photo.value = file
  removePhoto.value = false
  if (photoPreview.value) URL.revokeObjectURL(photoPreview.value)
  photoPreview.value = URL.createObjectURL(file)
}

function dropPhoto() {
  photo.value = null
  if (photoPreview.value) URL.revokeObjectURL(photoPreview.value)
  photoPreview.value = ''
  removePhoto.value = true
}

async function submit() {
  if (!form.title.trim() || !form.name.trim() || !form.contact.trim()) {
    error.value = 'Bitte Pflichtfelder und Textlängen prüfen.'
    return
  }
  saving.value = true
  error.value = ''
  let saved
  try {
    saved = props.item ? await api.update(props.item.id, form) : await api.create(form)
  } catch (e) {
    error.value = e.message
    saving.value = false
    return
  }
  rememberContact()
  let photoFailed = ''
  try {
    if (photo.value) saved = await api.putImage(saved.id, await shrink(photo.value))
    else if (removePhoto.value && props.item?.imageUrl) saved = await api.removeImage(saved.id)
  } catch (e) {
    photoFailed = e.message
  }
  saving.value = false
  emit('saved', saved, !props.item, photoFailed)
}

onMounted(() => {
  root.value?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  titleInput.value?.focus({ preventScroll: true })
})
onBeforeUnmount(() => { if (photoPreview.value) URL.revokeObjectURL(photoPreview.value) })
</script>

<template>
  <section ref="root" class="editor">
    <h2>{{ t(item ? 'Eintrag bearbeiten' : 'Was möchtest du ausleihen?') }}</h2>
    <p class="small">{{ t('* Pflichtfelder. Deine Angaben sind für die freigeschaltete Hausgemeinschaft sichtbar.') }}</p>
    <form @submit.prevent="submit">
      <div class="fields">
        <label>{{ t('Gegenstand') }} *
          <input ref="titleInput" v-model="form.title" maxlength="100" required :placeholder="t('Zum Beispiel: Bohrmaschine')">
        </label>
        <label>{{ t('Kategorie') }}
          <select v-model="form.category">
            <option v-for="c in categories" :key="c" :value="c">{{ t(c) }}</option>
          </select>
        </label>
        <label class="wide">{{ t('Beschreibung & Zubehör') }}
          <textarea v-model="form.description" maxlength="1200" rows="3" :placeholder="t('Was gehört dazu? Was sollte man wissen?')" />
        </label>
        <div v-if="pictures" class="wide photo-field">
          <span class="label">{{ t('Foto') }}</span>
          <div class="photo-row">
            <img v-if="shownPhoto" :src="shownPhoto" :alt="t('Foto von ') + form.title" class="photo-preview">
            <div class="photo-buttons">
              <label class="button">
                {{ t(shownPhoto ? 'Foto ersetzen' : 'Foto auswählen') }}
                <input type="file" accept="image/*" class="visually-hidden" @change="choose">
              </label>
              <button v-if="shownPhoto" type="button" @click="dropPhoto">{{ t('Foto entfernen') }}</button>
            </div>
          </div>
        </div>
        <label>{{ t('Dein Name') }} *
          <input v-model="form.name" maxlength="80" required :placeholder="t('Vorname oder Name am Klingelschild')">
        </label>
        <label>{{ t('Stockwerk / Wohnung') }}
          <input v-model="form.floor" maxlength="80" :placeholder="t('Zum Beispiel: 7. Stock, links')">
        </label>
        <label>{{ t('So erreicht man dich') }} *
          <input v-model="form.contact" maxlength="200" required :placeholder="t('Telefon, E-Mail oder: bitte klingeln')">
        </label>
        <label>{{ t('Verfügbarkeit') }}
          <select v-model="form.status">
            <option v-for="s in statuses" :key="s" :value="s">{{ t(s) }}</option>
          </select>
        </label>
        <label class="wide">{{ t('Ausleihbedingungen') }}
          <textarea v-model="form.conditions" maxlength="600" rows="2" :placeholder="t('Zum Beispiel: kostenlos, maximal 3 Tage, kurze Einweisung')" />
        </label>
      </div>
      <p v-if="error" class="notice error" role="alert">{{ t(error) }}</p>
      <div class="actions">
        <button class="primary" type="submit" :disabled="saving">
          {{ t(saving ? 'Wird gespeichert …' : item ? 'Änderungen speichern' : 'In die Liste eintragen') }}
        </button>
        <button type="button" :disabled="saving" @click="emit('cancel')">{{ t('Abbrechen') }}</button>
      </div>
    </form>
  </section>
</template>
