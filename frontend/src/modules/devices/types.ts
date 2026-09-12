export type VendorType = "fortigate" | "fortiweb";
export type DeviceStatus = "unknown" | "online" | "offline" | "error";

export interface Device {
  id: string;
  name: string;
  vendor_type: VendorType;
  host: string;
  port: number;
  verify_tls: boolean;
  vdom: string;
  status: DeviceStatus;
  last_checked_at: string | null;
  last_error: string;
  firmware_version: string;
  serial_number: string;
  reported_hostname: string;
  created_at: string;
}

export interface DeviceCreatePayload {
  name: string;
  vendor_type: VendorType;
  host: string;
  port?: number;
  verify_tls?: boolean;
  vdom?: string;
  api_token?: string;
  username?: string;
  password?: string;
}

export interface DeviceTestResult {
  success: boolean;
  status: DeviceStatus;
  firmware_version: string;
  serial_number: string;
  reported_hostname: string;
  error: string;
}
