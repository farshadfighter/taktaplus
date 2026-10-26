// Display helpers: Persian digits and the Jalali calendar via Intl.

const dateFmt = new Intl.DateTimeFormat('fa-IR-u-ca-persian', { year: 'numeric', month: '2-digit', day: '2-digit' })
const dateTimeFmt = new Intl.DateTimeFormat('fa-IR-u-ca-persian', {
  year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
})
const numFmt = new Intl.NumberFormat('fa-IR')

// Plain dates (YYYY-MM-DD) are calendar days; parse them at noon UTC so no
// time zone shifts them to another day.
function parse(v: string): Date {
  return /^\d{4}-\d{2}-\d{2}$/.test(v) ? new Date(v + 'T12:00:00Z') : new Date(v)
}

export const faDate = (v?: string | null) => (v ? dateFmt.format(parse(v)) : '—')
export const faDateTime = (v?: string | null) => (v ? dateTimeFmt.format(parse(v)) : '—')
export const faNum = (n?: number | null) => (n === null || n === undefined ? '—' : numFmt.format(n))

export function daysUntil(v?: string | null): number | null {
  if (!v) return null
  return Math.ceil((parse(v).getTime() - Date.now()) / 86_400_000)
}

// ISO date (YYYY-MM-DD) helpers for item dates.
export const isoToday = () => new Date().toISOString().slice(0, 10)
export function addYears(iso: string, years: number): string {
  const d = new Date(iso + 'T12:00:00Z')
  d.setUTCFullYear(d.getUTCFullYear() + years)
  d.setUTCDate(d.getUTCDate() - 1)
  return d.toISOString().slice(0, 10)
}

export const platformLabel = (p?: string | null) =>
  p === 'fortigate' ? 'FortiGate' : p === 'fortiweb' ? 'FortiWeb' : '—'
