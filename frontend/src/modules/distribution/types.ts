import { VendorType } from "@/modules/devices/types";

export type PackageSource = "ftp_sync" | "manual_upload";
export type PushStatus = "success" | "failed";

export interface Package {
  id: string;
  vendor_type: VendorType;
  package_type: string;
  filename: string;
  version: string;
  checksum_sha256: string;
  size_bytes: number;
  source: PackageSource;
  uploaded_by: string;
  fetched_at: string;
}

export interface PushRecord {
  id: string;
  device_id: string;
  package_id: string;
  pushed_at: string;
  pushed_by: string;
  status: PushStatus;
  error_message: string;
  post_push_check_ok: boolean | null;
}

export interface FtpSourceConfig {
  use_custom: boolean;
  host: string;
  port: number;
  username: string;
  remote_path: string;
}
