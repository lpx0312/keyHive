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

// 复制到剪贴板：navigator.clipboard 仅在安全上下文（HTTPS/localhost）存在，
// HTTP 局域网访问时为 undefined，需降级到 execCommand
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.focus()
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  } catch {
    return false
  }
}
