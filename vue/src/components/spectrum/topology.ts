/** Visual reference: https://58ol.com/ — sparse drift, proximity edges and local attraction.
 * Uses Spectrum tokens and independent lifecycle handling. At most 54 nodes: <= 1431 pair checks/frame. */
export interface TopologyNode {
  x: number
  y: number
  vx: number
  vy: number
  driftX: number
  driftY: number
  radius: number
  opacity: number
}

export interface TopologyPointer { x: number; y: number; active: boolean }

export function createTopologyNode(width: number, height: number): TopologyNode {
  const angle = Math.random() * Math.PI * 2
  const speed = 18 + Math.random() * 26
  const driftX = Math.cos(angle) * speed
  const driftY = Math.sin(angle) * speed
  return { x: Math.random() * width, y: Math.random() * height,
    vx: driftX, vy: driftY, driftX, driftY,
    radius: .55 + Math.random() * 1.35, opacity: .18 + Math.random() * .28 }
}

export function advanceTopology(nodes: TopologyNode[], pointer: TopologyPointer, width: number, height: number, seconds: number) {
  const settling = 1 - Math.exp(-1.1 * seconds)
  for (const node of nodes) {
    node.vx += (node.driftX - node.vx) * settling
    node.vy += (node.driftY - node.vy) * settling
    if (pointer.active) {
      const dx = pointer.x - node.x
      const dy = pointer.y - node.y
      const distanceSquared = dx * dx + dy * dy
      if (distanceSquared > .0001 && distanceSquared < 190 * 190) {
        const distance = Math.sqrt(distanceSquared)
        const pull = (1 - distance / 190) * 1000 * seconds / distance
        node.vx += dx * pull
        node.vy += dy * pull
      }
    }
    const speedSquared = node.vx * node.vx + node.vy * node.vy
    if (speedSquared > 220 * 220) {
      const scale = 220 / Math.sqrt(speedSquared)
      node.vx *= scale
      node.vy *= scale
    }
    node.x += node.vx * seconds
    node.y += node.vy * seconds
    if (node.x < -10) node.x = width + 10
    if (node.x > width + 10) node.x = -10
    if (node.y < -10) node.y = height + 10
    if (node.y > height + 10) node.y = -10
  }
}

export function paintTopology(context: CanvasRenderingContext2D, nodes: TopologyNode[], pointer: TopologyPointer, width: number, height: number, neutral: string, accent: string) {
  context.clearRect(0, 0, width, height)
  context.lineWidth = 1
  const dot = (x: number, y: number, radius: number, color: string, opacity: number) => {
    context.fillStyle = color
    context.globalAlpha = opacity
    context.beginPath()
    context.arc(x, y, radius, 0, Math.PI * 2)
    context.fill()
  }
  const edge = (x: number, y: number, endX: number, endY: number, color: string, opacity: number) => {
    context.strokeStyle = color
    context.globalAlpha = opacity
    context.beginPath()
    context.moveTo(x, y)
    context.lineTo(endX, endY)
    context.stroke()
  }
  for (let index = 0; index < nodes.length; index++) {
    const node = nodes[index]!
    for (let nextIndex = index + 1; nextIndex < nodes.length; nextIndex++) {
      const next = nodes[nextIndex]!
      const dx = node.x - next.x
      const dy = node.y - next.y
      const distanceSquared = dx * dx + dy * dy
      // Most pairs are distant. Only calculate a square root for visible edges.
      if (distanceSquared < 118 * 118) edge(node.x, node.y, next.x, next.y, neutral, (1 - Math.sqrt(distanceSquared) / 118) * .16)
    }
    dot(node.x, node.y, node.radius, neutral, node.opacity)
    if (pointer.active) {
      const dx = pointer.x - node.x
      const dy = pointer.y - node.y
      const distanceSquared = dx * dx + dy * dy
      if (distanceSquared < 210 * 210) {
        const opacity = (1 - Math.sqrt(distanceSquared) / 210) * .32
        edge(pointer.x, pointer.y, node.x, node.y, accent, opacity)
        dot(node.x, node.y, node.radius + .7, accent, Math.min(.5, opacity + .08))
      }
    }
  }
  if (pointer.active) dot(pointer.x, pointer.y, 2.2, accent, .5)
  context.globalAlpha = 1
}
