<script setup lang="ts">
import { DialogPortal, DialogOverlay, DialogContent, DialogClose, type DialogContentProps } from 'radix-vue'
import { X } from 'lucide-vue-next'
import { cn } from '@/lib/utils'
import { computed } from 'vue'

interface Props extends DialogContentProps {
  class?: string
  overlayClass?: string
}

const props = defineProps<Props>()
const delegatedProps = computed(() => {
  const { class: _class, overlayClass: _overlayClass, ...rest } = props
  return rest
})
</script>

<template>
  <DialogPortal>
    <DialogOverlay
      :class="cn('fixed inset-0 z-50 bg-black/80 data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0', props.overlayClass)"
    />
    <DialogContent
      v-bind="delegatedProps"
      :class="cn(
        'fixed left-[50%] top-[50%] z-50 grid w-full max-w-lg translate-x-[-50%] translate-y-[-50%] gap-4 border bg-background p-6 shadow-lg duration-[var(--duration-normal)] data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0 data-[state=closed]:zoom-out-95 data-[state=open]:zoom-in-95 data-[state=closed]:slide-out-to-left-1/2 data-[state=closed]:slide-out-to-top-[48%] data-[state=open]:slide-in-from-left-1/2 data-[state=open]:slide-in-from-top-[48%] sm:rounded-lg',
        props.class
      )"
    >
      <slot />
      <DialogClose
        class="absolute right-4 top-4 rounded-sm opacity-70 ring-offset-background transition-opacity hover:opacity-100 feedback-focus disabled:pointer-events-none data-[state=open]:bg-accent data-[state=open]:text-muted-foreground"
      >
        <X class="h-4 w-4" />
        <span class="sr-only">Close</span>
      </DialogClose>
    </DialogContent>
  </DialogPortal>
</template>
