from fastapi import APIRouter, Depends, Response
from sqlalchemy.orm import Session

from app.api.v1.deps import require_permission
from app.db.session import get_db
from app.domains.diagnostics.service import build_diagnostic_bundle

router = APIRouter(prefix="/diagnostics", tags=["diagnostics"])


@router.get("/bundle")
def download_bundle(
    db: Session = Depends(get_db),
    _=Depends(require_permission("diagnostics:export")),
) -> Response:
    content = build_diagnostic_bundle(db)
    return Response(
        content=content,
        media_type="application/zip",
        headers={"Content-Disposition": "attachment; filename=taktaplus-diagnostics.zip"},
    )
