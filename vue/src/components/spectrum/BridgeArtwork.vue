<script setup lang="ts">
import { ref, useId } from 'vue'
import bridgeArtwork from '@/assets/spectrum-bridge-rainbow.webp'

const failed = ref(false)
const ready = ref(false)
const maskId = `bridge-light-${useId()}`
const featherId = `${maskId}-feather`
// Coordinates follow the actual artwork. Both paths begin at a bridge foot and
// end at the crown; the same paths are mirrored across the waterline (y=375).
const arches = ['M 418 342 Q 662 140 930 140', 'M 1430 342 Q 1198 140 930 140']
</script>

<template>
  <!-- §2.4 / §7.1: keep the approved artwork and live HTML controls. Light travels
       through the architecture; feathered light and its reflection share a clock.
       Existing ink palette, one 900ms entrance, static reduced-motion fallback. -->
  <div v-if="!failed" class="spectrum-bridge-scenery" :class="{ 'is-ready': ready }" aria-hidden="true">
    <img class="spectrum-bridge-artwork" :src="bridgeArtwork" alt="" fetchpriority="high" width="1774" height="887" @load="ready = true" @error="failed = true" />
    <svg class="spectrum-bridge-lighting" viewBox="0 0 1774 887" preserveAspectRatio="xMidYMid slice" focusable="false">
      <defs>
        <filter :id="featherId" x="0" y="0" width="1774" height="887" filterUnits="userSpaceOnUse" color-interpolation-filters="sRGB">
          <feGaussianBlur stdDeviation="24" />
        </filter>
        <mask :id="maskId" x="0" y="0" width="1774" height="887" maskUnits="userSpaceOnUse" style="mask-type: luminance">
          <g :filter="`url(#${featherId})`" fill="none" stroke="white" stroke-width="180" stroke-linecap="round">
            <g class="spectrum-bridge-arch-light">
              <path v-for="arch in arches" :key="arch" class="spectrum-bridge-light-path" :d="arch" pathLength="1" />
            </g>
            <g class="spectrum-bridge-reflection-light" transform="translate(0 750) scale(1 -1)">
              <path v-for="arch in arches" :key="arch" class="spectrum-bridge-light-path" :d="arch" pathLength="1" />
            </g>
          </g>
          <rect class="spectrum-bridge-ambient-light" width="1774" height="887" fill="white" />
        </mask>
      </defs>
      <image :href="bridgeArtwork" width="1774" height="887" :mask="`url(#${maskId})`" />
    </svg>
  </div>
</template>
