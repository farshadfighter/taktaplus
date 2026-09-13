from app.core.security import decrypt_secret
from app.domains.devices.drivers.base import DeviceConnectionError, DeviceStatusInfo, FortinetDriver
from app.domains.devices.drivers.fortigate import FortiGateDriver
from app.domains.devices.drivers.fortiweb import FortiWebDriver
from app.domains.devices.models import Device, VendorType

__all__ = ["DeviceConnectionError", "DeviceStatusInfo", "FortinetDriver", "get_driver"]


def get_driver(device: Device) -> FortinetDriver:
    if device.vendor_type == VendorType.FORTIGATE:
        return FortiGateDriver(
            host=device.host,
            port=device.port,
            token=decrypt_secret(device.encrypted_api_token),
            verify_tls=device.verify_tls,
            vdom=device.vdom,
            ssh_port=device.ssh_port,
            ssh_username=device.ssh_username,
            ssh_password=decrypt_secret(device.encrypted_ssh_password) if device.encrypted_ssh_password else None,
        )
    if device.vendor_type == VendorType.FORTIWEB:
        return FortiWebDriver(
            host=device.host,
            port=device.port,
            username=device.username,
            password=decrypt_secret(device.encrypted_password),
            verify_tls=device.verify_tls,
        )
    raise ValueError(f"unsupported vendor_type: {device.vendor_type}")
