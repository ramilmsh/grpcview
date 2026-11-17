import { forwardRef, type ButtonHTMLAttributes } from "react";
import clsx from "clsx";

// "solid": the Invoke button only — the one filled CTA.
type Variant = "primary" | "secondary" | "danger" | "solid";

const VARIANT: Record<Variant, string> = {
  primary: "btn-primary",
  secondary: "btn-secondary",
  danger: "btn-danger",
  solid: "btn-solid",
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  ({ variant = "secondary", className, type = "button", ...rest }, ref) => (
    <button
      ref={ref}
      type={type}
      className={clsx("btn", VARIANT[variant], className)}
      {...rest}
    />
  ),
);
Button.displayName = "Button";

export const IconButton = forwardRef<
  HTMLButtonElement,
  ButtonHTMLAttributes<HTMLButtonElement>
>(({ className, type = "button", ...rest }, ref) => (
  <button
    ref={ref}
    type={type}
    className={clsx("iconbtn", className)}
    {...rest}
  />
));
IconButton.displayName = "IconButton";
