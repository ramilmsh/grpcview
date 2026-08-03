import { forwardRef, type InputHTMLAttributes, type ReactNode } from "react";
import clsx from "clsx";

export const Input = forwardRef<
  HTMLInputElement,
  InputHTMLAttributes<HTMLInputElement>
>(({ className, ...rest }, ref) => (
  <input ref={ref} className={clsx("input", className)} {...rest} />
));
Input.displayName = "Input";

export function Field({
  label,
  children,
  className,
}: {
  label: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={clsx("field", className)}>
      <label>{label}</label>
      {children}
    </div>
  );
}
