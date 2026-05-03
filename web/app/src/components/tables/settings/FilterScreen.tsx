import { Search } from 'lucide-react';
import { Button, Input } from '@/components/ui';

export function FilterScreen({
  filter,
  onFilterChange,
}: {
  filter: string;
  onFilterChange: (next: string) => void;
}) {
  return (
    <div className="space-y-3">
      <p className="text-sm text-foreground/55">Filter loaded rows by name.</p>
      <div className="relative">
        <Search
          className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-foreground/40"
          aria-hidden
        />
        <Input
          value={filter}
          onChange={(e) => onFilterChange(e.target.value)}
          placeholder="Filter by name..."
          className="h-9 pl-7 text-sm"
          aria-label="Settings filter rows"
        />
      </div>
      {filter.trim() !== '' && (
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => onFilterChange('')}
        >
          Clear filter
        </Button>
      )}
    </div>
  );
}
