import { onUnmounted, watch, type WatchSource } from 'vue'
import { sessionGeneration, sessionIdentity } from '@/lib/auth/session'

/** Invalidates completion handlers on close/reopen, target or identity changes. */
export function useDialogAttempt(source: WatchSource, reset: () => void) {
  let epoch = 0
  watch([source, sessionIdentity], () => { epoch++; reset() }, { flush: 'sync' })
  onUnmounted(() => { epoch++ })
  return () => {
    const started = epoch
    const identity = sessionGeneration()
    return () => started === epoch && identity === sessionGeneration()
  }
}
