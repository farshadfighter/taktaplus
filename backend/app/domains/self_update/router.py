from fastapi import APIRouter, Depends, HTTPException, UploadFile, status

from app.api.v1.deps import require_permission
from app.domains.self_update.service import (
    InvalidUpdatePackage,
    get_current_version,
    verify_package,
)

router = APIRouter(prefix="/self-update", tags=["self-update"])


@router.get("/version")
def current_version() -> dict:
    return {"version": get_current_version()}


@router.post("/verify")
async def verify_update_package(
    file: UploadFile,
    _=Depends(require_permission("self_update:manage")),
) -> dict:
    content = await file.read()
    try:
        manifest = verify_package(content)
    except InvalidUpdatePackage as exc:
        raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST, detail=str(exc)) from exc
    return {"version": manifest.version, "min_supported_version": manifest.min_supported_version}
