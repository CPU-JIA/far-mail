function randomIndex(length: number): number {
  const value = new Uint32Array(1)
  crypto.getRandomValues(value)
  return value[0] % length
}

export function pickRandomDomain(domains: string[]): string {
  if (!domains.length) return ''
  return domains[randomIndex(domains.length)]
}

export function addRandomSubdomain(rootDomain: string, levels: number): string {
  const letters = 'abcdefghijklmnopqrstuvwxyz'
  const characters = `${letters}0123456789`
  const count = Math.max(1, Math.min(5, Math.trunc(levels) || 1))
  const labels = Array.from({ length: count }, () => {
    const length = 3 + randomIndex(3)
    let label = letters[randomIndex(letters.length)]
    while (label.length < length) label += characters[randomIndex(characters.length)]
    return label
  })
  return `${labels.join('.')}.${rootDomain}`
}
