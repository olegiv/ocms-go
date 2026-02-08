import { useParams, Link } from 'react-router'
import { fetchPageBySlug } from '../api/client'
import { useApi } from '../hooks/useApi'

export default function PageDetail() {
  const { slug } = useParams<{ slug: string }>();

  const { data, loading, error } = useApi(
    () => fetchPageBySlug(slug!),
    [slug]
  );

  if (loading) return <div className="loading">Loading page...</div>;
  if (error) return <div className="error">{error}</div>;
  if (!data) return <div className="error">Page not found</div>;

  const page = data.data;

  return (
    <article className="page-detail">
      <Link to="/" className="back-link">Back to pages</Link>
      <h1>{page.title}</h1>
      <div className="page-meta">
        {page.author && <span>By {page.author.display_name}</span>}
        {page.published_at && (
          <time dateTime={page.published_at}>
            {new Date(page.published_at).toLocaleDateString()}
          </time>
        )}
      </div>
      {page.categories && page.categories.length > 0 && (
        <div className="category-list">
          {page.categories.map(c => (
            <span key={c.id} className="category">{c.name}</span>
          ))}
        </div>
      )}
      <div
        className="page-body"
        dangerouslySetInnerHTML={{ __html: page.body }}
      />
      {page.tags && page.tags.length > 0 && (
        <div className="tag-list">
          {page.tags.map(t => (
            <span key={t.id} className="tag">{t.name}</span>
          ))}
        </div>
      )}
    </article>
  );
}
