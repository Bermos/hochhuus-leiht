// A thin client for the Go API. Every failure becomes an Error whose message
// is the server's German sentence, ready for t().
async function request(method, path, body) {
  const init = { method, headers: {} }
  if (body instanceof FormData) {
    init.body = body
  } else if (body !== undefined) {
    init.headers['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }
  let res
  try {
    res = await fetch(path, init)
  } catch {
    throw new Error('Die Liste konnte nicht geladen werden. Bitte nochmals versuchen.')
  }
  if (res.status === 204) return null
  const data = await res.json().catch(() => ({}))
  if (!res.ok) {
    const err = new Error(data.error || (res.status === 413 ? 'Das Bild ist zu gross (maximal 15 MB).' : 'Anfrage abgelehnt.'))
    err.status = res.status
    throw err
  }
  return data
}

export const api = {
  session: () => request('GET', '/api/session'),
  login: (code) => request('POST', '/api/session', { code }),
  logout: () => request('DELETE', '/api/session'),
  items: () => request('GET', '/api/items'),
  create: (item) => request('POST', '/api/items', item),
  update: (id, item) => request('PUT', `/api/items/${encodeURIComponent(id)}`, item),
  remove: (id) => request('DELETE', `/api/items/${encodeURIComponent(id)}`),
  putImage: (id, blob) => {
    const form = new FormData()
    form.append('image', blob, 'photo.jpg')
    return request('PUT', `/api/items/${encodeURIComponent(id)}/image`, form)
  },
  removeImage: (id) => request('DELETE', `/api/items/${encodeURIComponent(id)}/image`),
  importBackup: (backup) => request('POST', '/api/import', backup),
}

// shrink scales a photo down in the browser before uploading it: phones take
// pictures far larger than a listing needs, and drawing to a canvas also
// applies the camera's rotation and leaves its location data behind.
export async function shrink(file, maxSide = 1600) {
  try {
    const bitmap = await createImageBitmap(file)
    const scale = Math.min(1, maxSide / Math.max(bitmap.width, bitmap.height))
    const canvas = document.createElement('canvas')
    canvas.width = Math.round(bitmap.width * scale)
    canvas.height = Math.round(bitmap.height * scale)
    const ctx = canvas.getContext('2d')
    ctx.fillStyle = '#fff'
    ctx.fillRect(0, 0, canvas.width, canvas.height)
    ctx.drawImage(bitmap, 0, 0, canvas.width, canvas.height)
    bitmap.close?.()
    const blob = await new Promise((resolve) => canvas.toBlob(resolve, 'image/jpeg', 0.85))
    return blob || file
  } catch {
    return file
  }
}
