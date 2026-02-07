import { fetchCategories } from '../api/client'
import { useApi } from '../hooks/useApi'
import type { Category } from '../types'

export default function CategoryTree() {
  const { data, loading, error } = useApi(
    () => fetchCategories(),
    []
  );

  if (loading) return <div className="loading">Loading categories...</div>;
  if (error) return <div className="error">{error}</div>;
  if (!data || data.data.length === 0) {
    return (
      <div className="empty">
        <h2>No categories found</h2>
        <p>Create some categories in the oCMS admin panel.</p>
      </div>
    );
  }

  return (
    <div>
      <h1>Categories</h1>
      <div className="category-tree">
        {data.data.map(cat => (
          <CategoryNode key={cat.id} category={cat} depth={0} />
        ))}
      </div>
    </div>
  );
}

function CategoryNode({ category, depth }: { category: Category; depth: number }) {
  return (
    <div className="category-node" style={{ marginLeft: depth * 24 }}>
      <div className="category-card">
        <h3>{category.name}</h3>
        <span className="category-count">{category.page_count} pages</span>
        {category.description && (
          <p className="category-desc">{category.description}</p>
        )}
      </div>
      {category.children?.map(child => (
        <CategoryNode key={child.id} category={child} depth={depth + 1} />
      ))}
    </div>
  );
}
