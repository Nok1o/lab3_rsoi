package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"libriary_system/gateway_service/internal/domain"
	"libriary_system/shared/pagination"
)

type fakeLibrary struct {
	library     domain.Library
	book        domain.LibraryBook
	available   int
	returnCalls int
}

func (service *fakeLibrary) ListLibrariesByCity(_ context.Context, city string, _ pagination.PageToken) (pagination.Page[domain.Library], error) {
	if city == service.library.City {
		return pagination.Page[domain.Library]{Items: []domain.Library{service.library}, Total: 1}, nil
	}
	return pagination.Page[domain.Library]{Items: []domain.Library{}}, nil
}

func (service *fakeLibrary) ListBooksByLibrary(_ context.Context, libraryUID uuid.UUID, _ bool, _ pagination.PageToken) (pagination.Page[domain.LibraryBook], error) {
	if libraryUID != service.library.Id {
		return pagination.Page[domain.LibraryBook]{}, domain.ErrLibraryNotFound
	}
	return pagination.Page[domain.LibraryBook]{Items: []domain.LibraryBook{service.book}, Total: 1}, nil
}

func (service *fakeLibrary) GetLibraryByUID(_ context.Context, libraryUID uuid.UUID) (domain.Library, error) {
	if libraryUID != service.library.Id {
		return domain.Library{}, domain.ErrLibraryNotFound
	}
	return service.library, nil
}

func (service *fakeLibrary) GetBookByUID(_ context.Context, libraryUID, bookUID uuid.UUID) (domain.LibraryBook, error) {
	if libraryUID != service.library.Id || bookUID != service.book.Id {
		return domain.LibraryBook{}, domain.ErrBookNotFound
	}
	book := service.book
	book.AvailableCount = service.available
	return book, nil
}

func (service *fakeLibrary) ReserveBook(ctx context.Context, libraryUID, bookUID uuid.UUID) (domain.LibraryBook, error) {
	book, err := service.GetBookByUID(ctx, libraryUID, bookUID)
	if err != nil {
		return domain.LibraryBook{}, err
	}
	if service.available == 0 {
		return domain.LibraryBook{}, domain.ErrBookUnavailable
	}
	service.available--
	book.AvailableCount = service.available
	return book, nil
}

func (service *fakeLibrary) ReturnBook(_ context.Context, libraryUID, bookUID uuid.UUID, condition domain.BookCondition) error {
	if libraryUID != service.library.Id || bookUID != service.book.Id {
		return domain.ErrBookNotFound
	}
	service.available++
	service.returnCalls++
	service.book.Condition = condition
	return nil
}

type fakeRating struct{ items map[string]domain.Rating }

func (service *fakeRating) GetByUsername(_ context.Context, username string) (domain.Rating, error) {
	rating, exists := service.items[username]
	if !exists {
		return domain.Rating{}, domain.ErrRatingNotFound
	}
	return rating, nil
}

func (service *fakeRating) Create(_ context.Context, rating domain.Rating) error {
	if _, exists := service.items[rating.Username]; exists {
		return domain.ErrRatingAlreadyExists
	}
	service.items[rating.Username] = rating
	return nil
}

func (service *fakeRating) AddStars(_ context.Context, username string, delta int) (domain.Rating, error) {
	rating, exists := service.items[username]
	if !exists {
		return domain.Rating{}, domain.ErrRatingNotFound
	}
	rating.StarsCount += delta
	if rating.StarsCount < 1 {
		rating.StarsCount = 1
	}
	if rating.StarsCount > 100 {
		rating.StarsCount = 100
	}
	service.items[username] = rating
	return rating, nil
}

type fakeReservation struct {
	items   map[uuid.UUID]domain.Reservation
	rentErr error
}

func (service *fakeReservation) ListByUsername(_ context.Context, username string) ([]domain.Reservation, error) {
	items := make([]domain.Reservation, 0)
	for _, item := range service.items {
		if item.Username == username {
			items = append(items, item)
		}
	}
	return items, nil
}

func (service *fakeReservation) CountByUsernameAndStatus(_ context.Context, username string, status domain.ReservationStatus) (int, error) {
	count := 0
	for _, item := range service.items {
		if item.Username == username && item.Status == status {
			count++
		}
	}
	return count, nil
}

func (service *fakeReservation) GetByUIDAndUsername(_ context.Context, reservationUID uuid.UUID, username string) (domain.Reservation, error) {
	item, exists := service.items[reservationUID]
	if !exists || item.Username != username {
		return domain.Reservation{}, domain.ErrReservationNotFound
	}
	return item, nil
}

func (service *fakeReservation) Rent(_ context.Context, username string, libraryUID, bookUID uuid.UUID, tillDate time.Time) (domain.Reservation, error) {
	if service.rentErr != nil {
		return domain.Reservation{}, service.rentErr
	}
	item := domain.Reservation{
		Id: uuid.New(), Username: username, LibraryId: libraryUID, BookId: bookUID,
		Status: domain.StatusRented, StartDate: time.Now().UTC(), TillDate: tillDate,
	}
	service.items[item.Id] = item
	return item, nil
}

func (service *fakeReservation) Return(_ context.Context, reservationUID uuid.UUID, username string, returnDate time.Time) (domain.Reservation, error) {
	item, err := service.GetByUIDAndUsername(context.Background(), reservationUID, username)
	if err != nil {
		return domain.Reservation{}, err
	}
	if item.Status != domain.StatusRented {
		return domain.Reservation{}, domain.ErrReservationNotRented
	}
	item.Status = domain.StatusReturned
	if returnDate.After(item.TillDate) {
		item.Status = domain.StatusExpired
	}
	service.items[item.Id] = item
	return item, nil
}

func newTestGateway() (*GatewayUseCase, *fakeLibrary, *fakeRating, *fakeReservation) {
	library := &fakeLibrary{
		library:   domain.Library{Id: uuid.New(), Name: "Library", City: "Москва", Address: "Address"},
		book:      domain.LibraryBook{Book: domain.Book{Id: uuid.New(), Name: "Book", Condition: domain.ConditionExcellent}},
		available: 1,
	}
	rating := &fakeRating{items: make(map[string]domain.Rating)}
	reservation := &fakeReservation{items: make(map[uuid.UUID]domain.Reservation)}
	return NewGatewayUseCase(library, rating, reservation), library, rating, reservation
}

func TestGetRatingDoesNotCreateMissingRating(t *testing.T) {
	gateway, _, rating, _ := newTestGateway()

	got, err := gateway.GetRating(context.Background(), "alice")
	if err != nil {
		t.Fatalf("GetRating() error = %v", err)
	}
	if got.Username != "alice" || got.StarsCount != initialStars {
		t.Fatalf("GetRating() = %+v", got)
	}
	if len(rating.items) != 0 {
		t.Fatalf("GET created rating: %+v", rating.items)
	}
}

func TestGatewayRentAndReturn(t *testing.T) {
	gateway, library, rating, reservationService := newTestGateway()
	ctx := context.Background()
	// The supplied Postman collection uses historical dates. The API contract
	// compares the return date with tillDate, not with the current clock.
	tillDate := time.Date(2021, 10, 11, 0, 0, 0, 0, time.UTC)
	details, err := gateway.Rent(ctx, "alice", library.library.Id, library.book.Id, tillDate)
	if err != nil || details.Reservation.Status != domain.StatusRented || details.Rating == nil || details.Rating.StarsCount != 75 {
		t.Fatalf("Rent() = (%+v, %v)", details, err)
	}
	if library.available != 0 || len(reservationService.items) != 1 {
		t.Fatalf("state after rent: available=%d, reservations=%d", library.available, len(reservationService.items))
	}
	items, err := gateway.ListReservations(ctx, "alice")
	if err != nil || len(items) != 1 || items[0].Book.Id != library.book.Id || items[0].Library.Id != library.library.Id {
		t.Fatalf("ListReservations() = (%+v, %v)", items, err)
	}
	if err := gateway.Return(ctx, details.Reservation.Id, "alice", domain.ConditionExcellent, tillDate); err != nil {
		t.Fatalf("Return() error = %v", err)
	}
	if library.available != 1 || rating.items["alice"].StarsCount != 76 {
		t.Fatalf("state after return: available=%d, rating=%d", library.available, rating.items["alice"].StarsCount)
	}
	if err := gateway.Return(ctx, details.Reservation.Id, "alice", domain.ConditionExcellent, tillDate); !errors.Is(err, domain.ErrReservationNotRented) {
		t.Fatalf("repeated Return() error = %v", err)
	}
	if library.returnCalls != 1 {
		t.Fatalf("library return calls = %d, want 1", library.returnCalls)
	}
}

func TestReturnPenalties(t *testing.T) {
	for _, check := range []struct {
		name       string
		returnDate time.Time
		condition  domain.BookCondition
		wantStars  int
	}{
		{"late", time.Date(2021, 10, 12, 0, 0, 0, 0, time.UTC), domain.ConditionExcellent, 65},
		{"condition changed", time.Date(2021, 10, 11, 0, 0, 0, 0, time.UTC), domain.ConditionBad, 65},
		{"both", time.Date(2021, 10, 12, 0, 0, 0, 0, time.UTC), domain.ConditionBad, 55},
	} {
		t.Run(check.name, func(t *testing.T) {
			gateway, library, rating, _ := newTestGateway()
			tillDate := time.Date(2021, 10, 11, 0, 0, 0, 0, time.UTC)
			details, err := gateway.Rent(context.Background(), "alice", library.library.Id, library.book.Id, tillDate)
			if err != nil {
				t.Fatal(err)
			}
			if err := gateway.Return(context.Background(), details.Reservation.Id, "alice", check.condition, check.returnDate); err != nil {
				t.Fatal(err)
			}
			if got := rating.items["alice"].StarsCount; got != check.wantStars {
				t.Fatalf("stars = %d, want %d", got, check.wantStars)
			}
		})
	}
}

func TestRentalLimitAndCompensation(t *testing.T) {
	gateway, library, rating, reservations := newTestGateway()
	rating.items["alice"] = domain.Rating{Username: "alice", StarsCount: 1}
	reservations.items[uuid.New()] = domain.Reservation{Username: "alice", Status: domain.StatusRented}
	tillDate := time.Date(2021, 10, 11, 0, 0, 0, 0, time.UTC)
	if _, err := gateway.Rent(context.Background(), "alice", library.library.Id, library.book.Id, tillDate); !errors.Is(err, ErrRentalLimitReached) {
		t.Fatalf("Rent() error = %v, want limit reached", err)
	}
	if library.available != 1 {
		t.Fatal("limit must not change availability")
	}
	reservations.items = make(map[uuid.UUID]domain.Reservation)
	reservations.rentErr = errors.New("database unavailable")
	if _, err := gateway.Rent(context.Background(), "alice", library.library.Id, library.book.Id, tillDate); err == nil {
		t.Fatal("Rent() error = nil, want database error")
	}
	if library.available != 1 || library.returnCalls != 1 {
		t.Fatalf("compensation failed: available=%d, returnCalls=%d", library.available, library.returnCalls)
	}
}
