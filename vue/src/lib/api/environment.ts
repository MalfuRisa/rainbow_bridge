import { BaseApiService } from './base-api'
import type { Environment } from './types'
import { apiClient } from './client'
import { toSnakeCase, toCamelCase } from '../utils'
import { assertEnvironment } from '@/lib/scope'

class EnvironmentApiService extends BaseApiService<Environment> {
  protected baseUrl = '/api/v1/environment'
  async update(data: Environment) {
    const environment = toSnakeCase(data) as Record<string, string>
    assertEnvironment(environment.environment_key!, environment.environment_id!)
    const response = await apiClient.post<{ environment: Environment }>(`${this.baseUrl}/update`, {
      environment, expected_environment_id: environment.environment_id,
    })
    return { environment: response.data?.environment ? toCamelCase(response.data.environment) as unknown as Environment : null }
  }

  protected getIdParamName(): string {
    return 'environment_key'
  }

  protected getRequestKey(): string {
    return 'environment'
  }

  protected getResponseKey(): string {
    return 'environment'
  }
}

export const environmentApi = new EnvironmentApiService()

