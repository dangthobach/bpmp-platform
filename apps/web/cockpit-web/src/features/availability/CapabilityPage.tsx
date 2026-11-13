import { ArrowRight, CircleHelp } from "lucide-react";
import { AppLink } from "../../routing/router";

export interface CapabilityLink { to: string; label: string }

export function CapabilityPage({
  title,
  summary,
  detail,
  links,
}: {
  title: string;
  summary: string;
  detail: string;
  links: CapabilityLink[];
}) {
  return (
    <>
      <header className="page-header"><div><h1>{title}</h1><p>{summary}</p></div></header>
      <section className="capability-panel" role="status">
        <span className="capability-panel__icon"><CircleHelp size={22} /></span>
        <div className="capability-panel__body">
          <h2>Some prototype actions do not have an owning API yet</h2>
          <p>{detail}</p>
          <div className="capability-panel__actions">
            {links.map(({ to, label }) => <AppLink key={to} className="button button--secondary" to={to}>{label}<ArrowRight size={14} /></AppLink>)}
          </div>
        </div>
      </section>
    </>
  );
}
