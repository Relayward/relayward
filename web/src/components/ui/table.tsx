import type { ComponentProps } from "react";

import { cn } from "@/lib/utils";

function Table({ className, ...props }: ComponentProps<"table">) {
  return (
    <div className="relative w-full overflow-x-auto">
      <table className={cn("w-full caption-bottom border-collapse text-sm", className)} {...props} />
    </div>
  );
}

function TableHeader({ className, ...props }: ComponentProps<"thead">) {
  return <thead className={cn("bg-muted/55 [&_tr]:border-b", className)} {...props} />;
}

function TableBody({ className, ...props }: ComponentProps<"tbody">) {
  return <tbody className={cn("[&_tr:last-child]:border-0", className)} {...props} />;
}

function TableRow({ className, ...props }: ComponentProps<"tr">) {
  return <tr className={cn("border-b border-border transition-colors hover:bg-muted/35", className)} {...props} />;
}

function TableHead({ className, ...props }: ComponentProps<"th">) {
  return <th className={cn("h-10 px-3.5 text-left align-middle text-xs font-semibold whitespace-nowrap text-muted-foreground", className)} {...props} />;
}

function TableCell({ className, ...props }: ComponentProps<"td">) {
  return <td className={cn("px-3.5 py-3 align-middle text-[13px]", className)} {...props} />;
}

export { Table, TableBody, TableCell, TableHead, TableHeader, TableRow };
