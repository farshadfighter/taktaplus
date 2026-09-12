from fastapi import APIRouter

from app.domains.diagnostics.router import router as diagnostics_router
from app.domains.identity.router import router as identity_router
from app.domains.licensing.router import router as licensing_router
from app.domains.self_update.router import router as self_update_router

api_router = APIRouter()
api_router.include_router(identity_router)
api_router.include_router(licensing_router)
api_router.include_router(diagnostics_router)
api_router.include_router(self_update_router)
