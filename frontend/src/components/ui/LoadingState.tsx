export function LoadingState({ label = "در حال بارگذاری..." }: { label?: string }) {
  return (
    <div className="loading-state">
      <span className="spinner" />
      {label}
    </div>
  );
}
