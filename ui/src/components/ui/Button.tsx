import { forwardRef, type ButtonHTMLAttributes } from "react";
import clsx from "clsx";

type Variant = "primary" | "secondary" | "danger";

const VARIANT: Record<Variant, string> = {
  primary: "btn-primary",
  secondary: "btn-secondary",
  danger: "btn-danger",
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
  )
);
Button.displayName = "Button";

export const IconButton = forwardRef<
  HTMLButtonElement,
  ButtonHTMLAttributes<HTMLButtonElement>
>(({ className, type = "button", ...rest }, ref) => (
  <button ref={ref} type={type} className={clsx("iconbtn", className)} {...rest} />
));
IconButton.displayName = "IconButton";
