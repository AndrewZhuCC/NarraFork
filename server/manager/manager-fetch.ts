/**
 * 管理器打到本机服务的请求。
 *
 * 健康检查、退出和更新打的是同一台刚探测过的服务。自签证书只在这一跳放行，
 * 不能让健康检查通过之后，关闭和更新因为证书校验失败。
 */
export function managerFetch(url: string, init: RequestInit = {}): Promise<Response> {
	return fetch(url, {
		...init,
		tls: { rejectUnauthorized: false },
	});
}
