import { NavLink, Outlet } from 'react-router-dom'

export default function Layout() {
  return (
    <div className="app">
      <header className="header">
        <div className="container">
          <NavLink to="/" className="logo">oCMS Headless</NavLink>
          <nav className="nav">
            <NavLink to="/" end>Pages</NavLink>
            <NavLink to="/media">Media</NavLink>
            <NavLink to="/tags">Tags</NavLink>
            <NavLink to="/categories">Categories</NavLink>
          </nav>
        </div>
      </header>
      <main className="main">
        <div className="container">
          <Outlet />
        </div>
      </main>
      <footer className="footer">
        <div className="container">
          Powered by oCMS Headless API
        </div>
      </footer>
    </div>
  )
}
