export type NodeDialogAction =
    | {kind: 'import'; url: string}
    | {kind: 'select'; game: string; http: string}
    | {kind: 'error'; message: string}

// 节点弹窗的提交决策单独放在纯函数里，避免 UI 状态变化再次让导入和选线互相阻塞。
export function decideNodeDialogAction(
    rawUrl: string,
    game?: string,
    http?: string,
): NodeDialogAction {
  const url = rawUrl.trim()
  if (url) {
    return {kind: 'import', url}
  }
  if (game === undefined) {
    return {kind: 'error', message: '请选择 Game 节点'}
  }
  if (http === undefined) {
    return {kind: 'error', message: '请选择 Http 节点'}
  }
  return {kind: 'select', game, http}
}

export function shouldConfirmHTTPDirect(_game?: string, http?: string): boolean {
  return http === '直连'
}

export function canDeletePeer(peer: {protocol: string}): boolean {
  return peer.protocol !== 'direct'
}
