import { ref } from 'vue'

// The signed-in user's alert language and the ones there are (from /api/me);
// null while signed out. The preferences menu changes it.
export const alertLang = ref<string | null>(null)
export const alertLangs = ref<string[]>([])

// Admin's "Mine / Everyone" choice on Searches. It lives outside the page so
// it survives a trip to the editor and back, and the editor's search
// switcher lists the same set.
export const showEveryone = ref(false)
