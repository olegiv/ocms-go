import type { PaginationMeta } from '../types'

interface Props {
  meta: PaginationMeta;
  onPageChange: (page: number) => void;
}

export default function Pagination({ meta, onPageChange }: Props) {
  if (meta.pages <= 1) return null;

  const pages: number[] = [];
  const start = Math.max(1, meta.page - 2);
  const end = Math.min(meta.pages, meta.page + 2);

  for (let i = start; i <= end; i++) {
    pages.push(i);
  }

  return (
    <div className="pagination">
      <button
        disabled={meta.page <= 1}
        onClick={() => onPageChange(meta.page - 1)}
      >
        Previous
      </button>

      {start > 1 && (
        <>
          <button onClick={() => onPageChange(1)}>1</button>
          {start > 2 && <span className="pagination-dots">...</span>}
        </>
      )}

      {pages.map(p => (
        <button
          key={p}
          className={p === meta.page ? 'active' : ''}
          onClick={() => onPageChange(p)}
        >
          {p}
        </button>
      ))}

      {end < meta.pages && (
        <>
          {end < meta.pages - 1 && <span className="pagination-dots">...</span>}
          <button onClick={() => onPageChange(meta.pages)}>{meta.pages}</button>
        </>
      )}

      <button
        disabled={meta.page >= meta.pages}
        onClick={() => onPageChange(meta.page + 1)}
      >
        Next
      </button>
    </div>
  )
}
