// F1.2's device fingerprint: a random ID minted once per browser and
// persisted in localStorage, not a real fingerprinting technique — good
// enough for "block trivial repeat trials," which is all this needs to be
// (the server's IP rate limit is the backstop against clearing storage).
const DEVICE_ID_KEY = 'aigc.device_id'

export function getDeviceId(): string {
  let id = localStorage.getItem(DEVICE_ID_KEY)
  if (!id) {
    id = crypto.randomUUID()
    localStorage.setItem(DEVICE_ID_KEY, id)
  }
  return id
}
