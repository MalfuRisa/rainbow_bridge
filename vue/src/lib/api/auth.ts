import { apiClient } from './client'
import type { AuthIdentity } from '../auth/session'

const options = { showErrorToast: false, handleAuth: false }
export const authApi = {
  async me(): Promise<AuthIdentity> {
    const response = await apiClient.request<AuthIdentity>('GET', '/api/v1/auth/me', undefined, options)
    return response.data as AuthIdentity
  },
  async login(username: string, password: string): Promise<AuthIdentity> {
    const response = await apiClient.request<AuthIdentity>('POST', '/api/v1/auth/login', { username, password }, options)
    return response.data as AuthIdentity
  },
  async logout(): Promise<void> {
    const response = await apiClient.request<{ logged_out: boolean }>('POST', '/api/v1/auth/logout', {}, options)
    if (response.data?.logged_out !== true) throw new Error('退出未完成，请重试')
  },
}
