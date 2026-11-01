import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { RoutingConfig } from '../api/types'

// Служба подменена: проверяем только поведение хранилища — что видит экран
// до ответа, после ответа и после отказа.
const api = vi.hoisted(() => ({
  getRouting: vi.fn(),
  batchRules: vi.fn(),
  setRoutingMode: vi.fn()
}))

vi.mock('../api/client', () => ({
  api,
  errorText: (e: unknown) => (e instanceof Error ? e.message : String(e))
}))

const notify = vi.hoisted(() => vi.fn())
vi.mock('./toasts', () => ({ notify, notifyError: notify }))

const { useRouting } = await import('./routing')

const config = (): RoutingConfig => ({
  mode: 'vpn_list',
  rules: [
    { id: '1', type: 'domain', value: 'a.com', enabled: true },
    { id: '2', type: 'domain', value: 'b.com', enabled: true }
  ]
})

// Обещание, которое тест разрешает сам: чтобы посмотреть на экран
// «посреди» запроса.
function deferred<T = unknown>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

describe('stores/routing', () => {
  beforeEach(async () => {
    vi.clearAllMocks()
    api.getRouting.mockResolvedValue(config())
    await useRouting().load()
  })

  it('убирает правило с экрана сразу, до ответа службы', async () => {
    const { rules, pending, remove } = useRouting()
    const reply = deferred()
    api.batchRules.mockReturnValue(reply.promise)

    const done = remove(rules.value[0]!)
    expect(rules.value.map(r => r.id)).toEqual(['2'])
    expect(pending.value).toBe(true)

    reply.resolve({ added: [], rejected: [], removed: 1 })
    await done
    expect(rules.value.map(r => r.id)).toEqual(['2'])
    expect(pending.value).toBe(false)
    expect(api.batchRules).toHaveBeenCalledWith([], ['1'])
  })

  it('возвращает правило и сообщает, если служба отказала', async () => {
    const { rules, remove } = useRouting()
    api.batchRules.mockRejectedValue(new Error('служба занята'))

    await remove(rules.value[0]!)
    expect(rules.value.map(r => r.id)).toEqual(['1', '2'])
    expect(notify).toHaveBeenCalledWith('Не удалось убрать a.com: служба занята')
  })

  it('не возвращает удаляемое правило, если список перечитали посреди удаления', async () => {
    const { rules, remove, load } = useRouting()
    const reply = deferred()
    api.batchRules.mockReturnValue(reply.promise)

    const done = remove(rules.value[0]!)
    await load()
    expect(rules.value.map(r => r.id)).toEqual(['2'])

    reply.resolve({ added: [], rejected: [], removed: 1 })
    await done
    expect(rules.value.map(r => r.id)).toEqual(['2'])
  })

  it('переключает режим сразу и откатывает при отказе', async () => {
    const { mode, setMode } = useRouting()
    const reply = deferred()
    api.setRoutingMode.mockReturnValue(reply.promise)

    const done = setMode('direct_list')
    expect(mode.value).toBe('direct_list')

    reply.reject(new Error('нет'))
    await done
    expect(mode.value).toBe('vpn_list')
  })
})
