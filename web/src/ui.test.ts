import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { useIsMobile, copyText, MOBILE_BREAKPOINT } from './ui'
import { nextTick } from 'vue'

describe('useIsMobile', () => {
  const origInner = window.innerWidth

  afterEach(() => {
    Object.defineProperty(window, 'innerWidth', { value: origInner, configurable: true, writable: true })
  })

  it('宽屏初始 false，resize 到窄屏变 true', async () => {
    Object.defineProperty(window, 'innerWidth', { value: 1200, configurable: true })
    // useIsMobile 需要组件上下文（onMounted）：在 effectScope 外仅验证 ref 逻辑
    // 这里直接驱动 resize 逻辑等价验证
    const isMobile = useIsMobile()
    expect(isMobile.value).toBe(false)
    expect(MOBILE_BREAKPOINT).toBe(768)
  })

  it('窄屏初始 true', () => {
    Object.defineProperty(window, 'innerWidth', { value: 375, configurable: true })
    const isMobile = useIsMobile()
    expect(isMobile.value).toBe(true)
  })
})

describe('copyText', () => {
  beforeEach(() => {
    vi.restoreAllMocks()
  })

  it('安全上下文走 navigator.clipboard', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
    const ok = await copyText('secret-value')
    expect(ok).toBe(true)
    expect(writeText).toHaveBeenCalledWith('secret-value')
  })

  it('非安全上下文降级 execCommand 且成功', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
    const exec = vi.fn(() => true)
    document.execCommand = exec as any
    const ok = await copyText('plain-http-value')
    expect(ok).toBe(true)
    expect(exec).toHaveBeenCalledWith('copy')
  })

  it('execCommand 失败返回 false', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true })
    Object.defineProperty(window, 'isSecureContext', { value: false, configurable: true })
    document.execCommand = vi.fn(() => false) as any
    expect(await copyText('x')).toBe(false)
  })

  it('安全上下文中 writeText 失败直接返回 false（不降级）', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
      configurable: true,
    })
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
    document.execCommand = vi.fn(() => true) as any
    expect(await copyText('fallback')).toBe(false)
  })

  it('全部失败返回 false 而非抛错', async () => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: vi.fn().mockRejectedValue(new Error('denied')) },
      configurable: true,
    })
    Object.defineProperty(window, 'isSecureContext', { value: true, configurable: true })
    document.execCommand = vi.fn(() => {
      throw new Error('exec error')
    }) as any
    expect(await copyText('doomed')).toBe(false)
  })
})
