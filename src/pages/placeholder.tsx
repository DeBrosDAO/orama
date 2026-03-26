import { Link, useLocation } from "react-router";
import { DashedPanel } from "../components/ui/dashed-panel";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";

export default function Placeholder() {
  const location = useLocation();

  return (
    <div className="flex items-center justify-center min-h-screen px-4">
      <DashedPanel withCorners withBackground className="max-w-md w-full text-center">
        <div className="flex flex-col items-center gap-6">
          <Badge variant="outline">UNDER CONSTRUCTION</Badge>
          <p className="font-mono text-sm text-muted">
            <span className="text-accent">{location.pathname}</span>
          </p>
          <Button asChild variant="ghost" size="sm">
            <Link to="/">Back to Home</Link>
          </Button>
        </div>
      </DashedPanel>
    </div>
  );
}
