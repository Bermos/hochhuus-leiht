<script setup>
import { t } from '../i18n.js'

defineProps({ item: { type: Object, required: true } })
defineEmits(['edit', 'remove'])
</script>

<template>
  <article class="card">
    <img v-if="item.imageUrl" :src="item.imageUrl" :alt="t('Foto von ') + item.title" class="card-photo" loading="lazy">
    <span class="tag" :class="'status-' + item.status.replace(/\s+/g, '-').toLowerCase()">{{ t(item.status) }}</span>
    <span v-if="item.mine" class="tag mine">{{ t('Mein Eintrag') }}</span>
    <p class="category">{{ t(item.category) }}</p>
    <h3>{{ item.title }}</h3>
    <p v-if="item.description">{{ item.description }}</p>
    <p class="person">{{ item.name }}<template v-if="item.floor"> · {{ item.floor }}</template></p>
    <details>
      <summary>{{ t('Kontakt & Ausleihe') }}</summary>
      <p>{{ t('Kontakt') }}: {{ item.contact }}</p>
      <p>{{ t('Bedingungen') }}: {{ item.conditions || t('Bitte direkt miteinander vereinbaren.') }}</p>
      <p class="small">{{ t('Termin, Rückgabe und Zustand direkt absprechen.') }}</p>
    </details>
    <div v-if="item.editable" class="actions">
      <button type="button" @click="$emit('edit', item)">{{ t('Bearbeiten') }}</button>
      <button type="button" @click="$emit('remove', item)">{{ t('Entfernen') }}</button>
    </div>
  </article>
</template>
