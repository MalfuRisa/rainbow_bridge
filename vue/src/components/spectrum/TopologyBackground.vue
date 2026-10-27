<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { advanceTopology, createTopologyNode, paintTopology, type TopologyNode, type TopologyPointer } from './topology'

const canvas = ref<HTMLCanvasElement | null>(null)
let dispose = () => {}

onMounted(() => {
  const surface = canvas.value
  const context = surface?.getContext('2d')
  if (!surface || !context) return

  const motion = window.matchMedia('(prefers-reduced-motion: reduce)')
  const finePointer = window.matchMedia('(pointer: fine)')
  const nodes: TopologyNode[] = []
  const pointer: TopologyPointer = { x: 0, y: 0, active: false }
  let targetX = 0
  let targetY = 0
  let width = 0
  let height = 0
  let frame = 0
  let previous: number | null = null
  let neutral = ''
  let accent = ''
  let disposed = false
  const shouldAnimate = () => !disposed && !motion.matches && finePointer.matches && !document.hidden
  const paint = () => paintTopology(context, nodes, pointer, width, height, neutral, accent)
  const clearPointer = () => { pointer.active = false }

  function tick(time: number) {
    frame = 0
    if (!shouldAnimate()) return
    const seconds = Math.min(previous === null ? 0 : time - previous, 50) / 1000
    previous = time
    // Paint every display refresh. Small time-based steps keep attraction stable
    // across 60/120/144 Hz and avoid jumps after a delayed frame.
    const steps = Math.max(1, Math.ceil(seconds * 120))
    const step = seconds / steps
    const easing = 1 - Math.exp(-20 * step)
    for (let index = 0; index < steps; index++) {
      pointer.x += (targetX - pointer.x) * easing
      pointer.y += (targetY - pointer.y) * easing
      advanceTopology(nodes, pointer, width, height, step)
    }
    paint()
    frame = requestAnimationFrame(tick)
  }

  function syncMotion() {
    cancelAnimationFrame(frame)
    frame = 0
    previous = null
    clearPointer()
    paint()
    if (shouldAnimate()) frame = requestAnimationFrame(tick)
  }

  function resize() {
    const previousWidth = width || innerWidth
    const previousHeight = height || innerHeight
    width = innerWidth
    height = innerHeight
    const ratio = Math.min(devicePixelRatio || 1, 2)
    surface!.width = Math.round(width * ratio)
    surface!.height = Math.round(height * ratio)
    context!.setTransform(ratio, 0, 0, ratio, 0, 0)
    for (const node of nodes) { node.x *= width / previousWidth; node.y *= height / previousHeight }
    const count = Math.max(28, Math.min(54, Math.round(width * height / 28000)))
    while (nodes.length < count) nodes.push(createTopologyNode(width, height))
    nodes.length = count
    clearPointer()
    paint()
  }

  function updatePalette() {
    const style = getComputedStyle(surface!)
    neutral = style.getPropertyValue('--muted-foreground').trim()
    accent = style.getPropertyValue('--spectrum-link').trim()
    paint()
  }

  function movePointer(event: PointerEvent) {
    if (!shouldAnimate() || event.pointerType === 'touch') return
    // Input and modal work takes precedence over background feedback.
    if (event.target instanceof Element && event.target.closest('input, textarea, [contenteditable="true"], [role="dialog"]')) {
      clearPointer()
      return
    }
    targetX = event.clientX
    targetY = event.clientY
    if (!pointer.active) { pointer.x = targetX; pointer.y = targetY }
    pointer.active = true
  }

  const themeObserver = new MutationObserver(updatePalette)
  themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })
  window.addEventListener('resize', resize, { passive: true })
  window.addEventListener('pointermove', movePointer, { passive: true })
  window.addEventListener('blur', clearPointer)
  document.addEventListener('pointerleave', clearPointer)
  window.addEventListener('keydown', clearPointer)
  document.addEventListener('visibilitychange', syncMotion)
  motion.addEventListener('change', syncMotion)
  finePointer.addEventListener('change', syncMotion)
  updatePalette()
  resize()
  syncMotion()

  dispose = () => {
    disposed = true
    cancelAnimationFrame(frame)
    themeObserver.disconnect()
    window.removeEventListener('resize', resize)
    window.removeEventListener('pointermove', movePointer)
    window.removeEventListener('blur', clearPointer)
    document.removeEventListener('pointerleave', clearPointer)
    window.removeEventListener('keydown', clearPointer)
    document.removeEventListener('visibilitychange', syncMotion)
    motion.removeEventListener('change', syncMotion)
    finePointer.removeEventListener('change', syncMotion)
  }
})

onUnmounted(() => dispose())
</script>

<template>
  <!-- §2.4 / §5.7: a quiet decorative layer under opaque task surfaces; no input capture. -->
  <canvas ref="canvas" class="spectrum-topology" aria-hidden="true" />
</template>

<style scoped>
.spectrum-topology { position: fixed; inset: 0; width: 100%; height: 100%; z-index: -1; pointer-events: none; }
</style>
