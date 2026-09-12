export type BackupSource = "manual" | "scheduled";
export type BackupStatus = "success" | "failed";

export interface Backup {
  id: string;
  device_id: string;
  taken_at: string;
  taken_by: string;
  source: BackupSource;
  status: BackupStatus;
  error_message: string;
  checksum_sha256: string;
  size_bytes: number;
  device_serial_snapshot: string;
  device_firmware_snapshot: string;
}
