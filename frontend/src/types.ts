export type Role = 'superadmin' | 'tenant';

export interface User {
  id: number;
  username: string;
  role: Role;
  active: boolean;
  max_servers: number;
  max_tunnels: number;
  max_monitors: number;
  expires_at?: string;
  created_at: string;
}

export interface Branding {
  app_name: string;
  subtitle: string;
  accent: string;
}

export interface ThemePrefs {
  preset: string;
  mode: 'dark' | 'light';
  accent: string;
  radius: number;
  lang: 'fa' | 'en';
}

export interface ServerItem {
  id: number;
  name: string;
  role: 'iran' | 'foreign';
  host: string;
  port: number;
  username: string;
  country: string;
  status: string;
  sort_order: number;
  pos_x: number;
  pos_y: number;
  created_at: string;
}

export interface TunnelItem {
  id: number;
  name: string;
  engine: string;
  iran_server_id: number;
  foreign_server_id: number;
  tunnel_port: number;
  ports: string;
  status: string;
  created_at: string;
}

export interface SystemStats {
  total_users: number;
  active_tenants: number;
  total_servers: number;
  total_tunnels: number;
}
