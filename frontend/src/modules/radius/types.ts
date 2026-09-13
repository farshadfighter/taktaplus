export type SmsProvider = "kavenegar" | "generic_http";

export interface TwoFactorUser {
  id: string;
  username: string;
  enabled: boolean;
  failed_attempts: number;
  locked_until: string | null;
  created_at: string;
}

export interface RadiusClient {
  id: string;
  name: string;
  nas_ip: string;
  enabled: boolean;
  created_at: string;
}

export interface SmsGatewayConfig {
  provider: SmsProvider;
  kavenegar_sender: string | null;
  generic_method: string;
  generic_url_template: string | null;
  generic_auth_header_name: string | null;
  has_kavenegar_api_key: boolean;
  has_generic_auth_header_value: boolean;
}
