import { ref, onMounted, onUnmounted } from 'vue'

// 响应式断点：窄屏走移动布局
export const MOBILE_BREAKPOINT = 768

export function useIsMobile() {
  const isMobile = ref(typeof window !== 'undefined' && window.innerWidth <= MOBILE_BREAKPOINT)
  const onResize = () => {
    isMobile.value = window.innerWidth <= MOBILE_BREAKPOINT
  }
  onMounted(() => window.addEventListener('resize', onResize))
  onUnmounted(() => window.removeEventListener('resize', onResize))
  return isMobile
}
