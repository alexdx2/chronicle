export interface DatabaseConfig {
  host: string;
  port: number;
  database: string;
  username: string;
  password: string;
}

export interface KafkaConfig {
  brokers: string[];
  clientId: string;
  groupId: string;
}

export interface ServiceConfig {
  tomApiUrl: string;
  jerryApiUrl: string;
  arenaApiUrl: string;
  redisUrl: string;
  notificationServiceUrl: string;
}

export function getServiceConfig(): ServiceConfig {
  return {
    tomApiUrl: process.env.TOM_API_URL || 'http://tom-api:3001',
    jerryApiUrl: process.env.JERRY_API_URL || 'http://jerry-api:3002',
    arenaApiUrl: process.env.ARENA_API_URL || 'http://arena-api:3003',
    redisUrl: process.env.REDIS_URL || 'redis://localhost:6379',
    notificationServiceUrl: process.env.NOTIFICATION_SERVICE_URL || 'http://notifications:3005',
  };
}
