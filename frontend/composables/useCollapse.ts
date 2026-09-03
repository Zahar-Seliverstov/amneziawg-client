// Раскрытие и схлопывание строки списка по её собственной высоте.
//
// CSS не умеет переходить от height: auto, а max-height «на глаз» не годится
// ни в какую сторону: возьмёшь с запасом — строка первую половину перехода
// стоит на месте и только потом проваливается, возьмёшь впритык — режет всё,
// что выше. Настоящую высоту знает только браузер, поэтому её и спрашиваем.
//
// Само движение остаётся в CSS (.list-enter-active/.list-leave-active) —
// отсюда приходит одно недостающее число. Вешается на список целиком:
// <TransitionGroup name="list" v-bind="collapse">.

// Кадр нужен, чтобы браузер успел принять начальное значение: поставленные в
// одном кадре начало и конец он видит как одно изменение и перехода не
// запускает. Два кадра — столько же, сколько ждёт сам Vue, прежде чем снять
// класс начального состояния: движение должно начаться разом.
function nextFrame(run: () => void) {
  requestAnimationFrame(() => requestAnimationFrame(run))
}

function expand(el: Element) {
  const row = el as HTMLElement
  const height = row.offsetHeight
  const { paddingTop, paddingBottom } = getComputedStyle(row)

  row.style.height = '0px'
  row.style.paddingTop = '0px'
  row.style.paddingBottom = '0px'

  nextFrame(() => {
    row.style.height = `${height}px`
    row.style.paddingTop = paddingTop
    row.style.paddingBottom = paddingBottom
  })
}

function collapseRow(el: Element) {
  const row = el as HTMLElement
  row.style.height = `${row.offsetHeight}px`

  nextFrame(() => {
    row.style.height = '0px'
    row.style.paddingTop = '0px'
    row.style.paddingBottom = '0px'
  })
}

// Высота нужна была только на время перехода: дальше строка живёт своей.
function release(el: Element) {
  const row = el as HTMLElement
  row.style.height = ''
  row.style.paddingTop = ''
  row.style.paddingBottom = ''
}

export const collapse = {
  onEnter: expand,
  onAfterEnter: release,
  onEnterCancelled: release,
  onLeave: collapseRow,
  onAfterLeave: release,
  onLeaveCancelled: release
}
